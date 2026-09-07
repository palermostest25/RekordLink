package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rekordlink/rekordlink/internal/audio"
	"github.com/rekordlink/rekordlink/internal/library"
)

type JoinConfig struct {
	ExcludePlaylists []string
	SharedPlaylists  []string
	Invite           string
	LibraryPath      string
	Name             string
	OutputPath       string
	StateDir         string
	Interval         time.Duration
	Version          string
	AudioRoot        string
}

type clientState struct {
	RoomID      string `json:"room_id"`
	Endpoint    string `json:"endpoint"`
	Fingerprint string `json:"fingerprint"`
	Transport   string `json:"transport,omitempty"`
	PeerID      string `json:"peer_id"`
	Token       string `json:"token"`
	Name        string `json:"name"`
}

type syncClient struct {
	excludePlaylists []string
	sharedPlaylists  []string
	http             *http.Client
	state            clientState
	statePath        string
	library          string
	output           string
	lastSource       [32]byte
	etag             string
	lastMerged       [32]byte
	hasLastMerged    bool
	statusFallback   bool
	observedRevision string
	audio            *audio.Manager
}

func RunJoin(ctx context.Context, cfg JoinConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	inv, err := decodeInvite(cfg.Invite)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.StateDir, 0o700); err != nil {
		return err
	}
	statePath := filepath.Join(cfg.StateDir, "client.json")
	state, err := loadClientState(statePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state.RoomID != "" && state.RoomID != inv.RoomID {
		return errors.New("saved join state belongs to another room; choose another --state directory")
	}
	httpClient := clientForInvite(inv)
	if inv.Audio != (cfg.AudioRoot != "") {
		return errors.New("host and guest must either both enable audio sync or both disable it")
	}
	var audioManager *audio.Manager
	if cfg.AudioRoot != "" {
		audioManager, err = newClientAudioManager(cfg.AudioRoot, cfg.StateDir)
		if err != nil {
			return err
		}
	}
	if state.Token == "" {
		state, err = registerClient(ctx, httpClient, inv, cfg.Name)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(state, "", "  ")
		if err := WriteAtomic(statePath, append(b, '\n'), 0o600); err != nil {
			return err
		}
	} else {
		state.Endpoint = inv.Endpoint
		state.Fingerprint = inv.Fingerprint
		state.Transport = inv.Transport
		state.Name = cfg.Name
		b, _ := json.MarshalIndent(state, "", "  ")
		if err := WriteAtomic(statePath, append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	c := &syncClient{http: httpClient, state: state, statePath: statePath, library: cfg.LibraryPath, output: cfg.OutputPath, audio: audioManager, excludePlaylists: cfg.ExcludePlaylists, sharedPlaylists: cfg.SharedPlaylists}
	log.Printf("paired with room %s; writing merged library to %s", state.RoomID, cfg.OutputPath)
	if len(cfg.SharedPlaylists) > 0 {
		if err := c.requireSharedPlaylistSupport(ctx); err != nil {
			return err
		}
	}
	if err := c.pushIfChanged(ctx, true); err != nil {
		return err
	}
	if err := c.pull(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.pushIfChanged(ctx, false); err != nil {
				log.Printf("publish retry: %v", err)
			}
			if err := c.pull(ctx); err != nil {
				log.Printf("download retry: %v", err)
			}
		}
	}
}

func (c *syncClient) requireSharedPlaylistSupport(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.state.Endpoint+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("check shared-library support: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("the host or relay could not confirm shared-library support")
	}
	var health struct {
		SharedPlaylists bool `json:"shared_playlists"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health); err != nil || !health.SharedPlaylists {
		return errors.New("shared Duo Library requires an updated v0.5.0 host or relay")
	}
	return nil
}

func loadClientState(path string) (clientState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return clientState{}, err
	}
	var state clientState
	if err := json.Unmarshal(b, &state); err != nil {
		return clientState{}, fmt.Errorf("read client state: %w", err)
	}
	return state, nil
}

func registerClient(ctx context.Context, client *http.Client, inv invitation, name string) (clientState, error) {
	body, _ := json.Marshal(registerRequest{Name: name, PairCode: inv.PairCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inv.Endpoint+"/api/v1/register", bytes.NewReader(body))
	if err != nil {
		return clientState{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return clientState{}, fmt.Errorf("pair with host: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return clientState{}, responseError(resp)
	}
	var registered registerResponse
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		return clientState{}, err
	}
	if registered.RoomID != inv.RoomID {
		return clientState{}, errors.New("host returned the wrong room ID")
	}
	return clientState{RoomID: inv.RoomID, Endpoint: inv.Endpoint, Fingerprint: inv.Fingerprint, Transport: inv.Transport, PeerID: registered.PeerID, Token: registered.Token, Name: name}, nil
}

func clientForInvite(inv invitation) *http.Client {
	if inv.Transport == transportPublicTLS {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		transport.ForceAttemptHTTP2 = true
		return &http.Client{Transport: transport, Timeout: 35 * time.Minute, CheckRedirect: rejectRedirect}
	}
	return pinnedClient(inv.Fingerprint)
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("relay redirects are not allowed")
}

func pinnedClient(fingerprint string) *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Replaced by explicit certificate pin validation below.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("host supplied no TLS certificate")
			}
			actual := certFingerprint(cs.PeerCertificates[0].Raw)
			if !subtleEqual(actual, fingerprint) {
				return errors.New("host certificate does not match the invite")
			}
			return nil
		},
	}}
	return &http.Client{Transport: transport, Timeout: 35 * time.Minute, CheckRedirect: rejectRedirect}
}

func (c *syncClient) pushIfChanged(ctx context.Context, force bool) error {
	b, err := os.ReadFile(c.library)
	if err != nil {
		return err
	}
	if len(b) > maxXMLBytes {
		return errors.New("library XML exceeds the 128 MiB safety limit")
	}
	sum := sha256.Sum256(b)
	if !force && sum == c.lastSource {
		return nil
	}
	b, err = preparePublishedSnapshot(b, c.excludePlaylists, c.sharedPlaylists)
	if err != nil {
		return err
	}
	if c.audio != nil {
		prepared, err := c.audio.Prepare(b)
		if err != nil {
			return err
		}
		logAudioWarnings(prepared.Warnings)
		for _, hash := range prepared.Hashes {
			if err := c.uploadBlob(ctx, hash); err != nil {
				return err
			}
		}
		b = prepared.XML
	}
	if err := c.publishSnapshot(ctx, b); err != nil {
		return err
	}
	c.lastSource = sum
	log.Printf("published updated library")
	return nil
}

func (c *syncClient) publishSnapshot(ctx context.Context, data []byte) error {
	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])
	endpoint := c.state.Endpoint + "/api/v1/snapshot-uploads/" + hash
	head, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return err
	}
	c.authorize(head)
	resp, err := c.http.Do(head)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return c.publishSnapshotLegacy(ctx, data)
	}
	if resp.StatusCode != http.StatusOK {
		err := responseError(resp)
		resp.Body.Close()
		return err
	}
	offset, err := strconv.ParseInt(resp.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
	resp.Body.Close()
	if err != nil || offset < 0 || offset > int64(len(data)) {
		return errors.New("relay returned an invalid resumable snapshot offset")
	}
	for {
		remaining := int64(len(data)) - offset
		chunkSize := min(remaining, transferChunkBytes)
		put, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data[offset:offset+chunkSize]))
		if err != nil {
			return err
		}
		put.ContentLength = chunkSize
		put.Header.Set("Content-Type", "application/xml")
		put.Header.Set("X-RekordLink-Name", c.state.Name)
		put.Header.Set("X-RekordLink-Upload-Offset", strconv.FormatInt(offset, 10))
		put.Header.Set("X-RekordLink-Upload-Length", strconv.Itoa(len(data)))
		c.authorize(put)
		resp, err := c.http.Do(put)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusConflict {
			newOffset, parseErr := strconv.ParseInt(resp.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
			resp.Body.Close()
			if parseErr != nil || newOffset < 0 || newOffset > int64(len(data)) {
				return errors.New("relay returned an invalid resumable snapshot offset")
			}
			offset = newOffset
			continue
		}
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			err := responseError(resp)
			resp.Body.Close()
			return err
		}
		complete := resp.Header.Get("X-RekordLink-Upload-Complete") == "true"
		newOffset, parseErr := strconv.ParseInt(resp.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
		resp.Body.Close()
		if parseErr != nil || newOffset < offset || newOffset > int64(len(data)) {
			return errors.New("relay returned an invalid resumable snapshot offset")
		}
		offset = newOffset
		if complete {
			return nil
		}
	}
}

func (c *syncClient) publishSnapshotLegacy(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.state.Endpoint+"/api/v1/snapshot", bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("X-RekordLink-Name", c.state.Name)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return responseError(resp)
	}
	return nil
}

func (c *syncClient) pull(ctx context.Context) error {
	remoteRevision := ""
	if c.statusFallback {
		if revision, err := c.fetchRemoteRevision(ctx); err == nil {
			if c.observedRevision != "" && revision == c.observedRevision {
				return nil
			}
			remoteRevision = revision
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.state.Endpoint+"/api/v1/combined.xml", nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if responseETag := normalizeETag(resp.Header.Get("ETag")); responseETag != "" {
			c.etag = responseETag
		}
		if remoteRevision != "" {
			c.observedRevision = remoteRevision
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}
	responseETag := normalizeETag(resp.Header.Get("ETag"))
	if responseETag != "" && c.etag != "" && responseETag == c.etag {
		_ = resp.Body.Close()
		c.enableStatusFallback(ctx, remoteRevision)
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxXMLBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxXMLBytes {
		return errors.New("merged XML exceeds the 128 MiB safety limit")
	}
	if c.audio != nil {
		b, err = c.audio.Materialize(b, func(hash, destination string) error {
			if _, ok := c.audio.HasBlob(hash); ok {
				return audio.CopyVerified(c.audio.BlobPath(hash), destination, hash)
			}
			return c.downloadBlob(ctx, hash, destination)
		})
		if err != nil {
			return err
		}
	}
	mergedHash := sha256.Sum256(b)
	if responseETag == "" {
		c.enableStatusFallback(ctx, remoteRevision)
		remoteRevision = c.observedRevision
	}
	receiptRevision := responseETag
	if receiptRevision == "" {
		receiptRevision = remoteRevision
	}
	if c.hasLastMerged && mergedHash == c.lastMerged {
		c.etag = responseETag
		if remoteRevision != "" {
			c.observedRevision = remoteRevision
		}
		return nil
	}
	if err := WriteAtomic(c.output, b, 0o600); err != nil {
		return err
	}
	if err := WriteReceipt(c.output, b, receiptRevision); err != nil {
		return err
	}
	c.etag = responseETag
	c.lastMerged = mergedHash
	c.hasLastMerged = true
	if remoteRevision != "" {
		c.observedRevision = remoteRevision
	}
	log.Printf("updated merged library")
	return nil
}

func (c *syncClient) enableStatusFallback(ctx context.Context, knownRevision string) {
	c.statusFallback = true
	if knownRevision == "" {
		knownRevision, _ = c.fetchRemoteRevision(ctx)
	}
	if knownRevision != "" {
		c.observedRevision = knownRevision
	}
}

func (c *syncClient) fetchRemoteRevision(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.state.Endpoint+"/api/v1/status", nil)
	if err != nil {
		return "", err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", responseError(resp)
	}
	var roomStatus status
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&roomStatus); err != nil {
		return "", err
	}
	if roomStatus.RoomID == "" {
		return "", errors.New("relay status has no room ID")
	}
	return fmt.Sprintf("%s-%d", roomStatus.RoomID, roomStatus.Revision), nil
}

func (c *syncClient) uploadBlob(ctx context.Context, hash string) error {
	head, err := http.NewRequestWithContext(ctx, http.MethodHead, c.state.Endpoint+"/api/v1/blobs/"+hash, nil)
	if err != nil {
		return err
	}
	c.authorize(head)
	resp, err := c.http.Do(head)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		err := responseError(resp)
		resp.Body.Close()
		return err
	}
	resp.Body.Close()
	if err := c.uploadBlobChunks(ctx, hash); !errors.Is(err, errChunkAPIUnavailable) {
		return err
	}
	return c.uploadBlobLegacy(ctx, hash)
}

var errChunkAPIUnavailable = errors.New("resumable transfer API unavailable")

func (c *syncClient) uploadBlobChunks(ctx context.Context, hash string) error {
	f, err := os.Open(c.audio.BlobPath(hash))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	for {
		head, err := http.NewRequestWithContext(ctx, http.MethodHead, c.state.Endpoint+"/api/v1/uploads/"+hash, nil)
		if err != nil {
			return err
		}
		c.authorize(head)
		resp, err := c.http.Do(head)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return errChunkAPIUnavailable
		}
		if resp.StatusCode != http.StatusOK {
			err := responseError(resp)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		if resp.Header.Get("X-RekordLink-Upload-Complete") == "true" {
			return nil
		}
		offset, err := strconv.ParseInt(resp.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
		if err != nil || offset < 0 || offset > info.Size() {
			return errors.New("relay returned an invalid resumable upload offset")
		}
		remaining := info.Size() - offset
		chunkSize := min(remaining, transferChunkBytes)
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		put, err := http.NewRequestWithContext(ctx, http.MethodPut, c.state.Endpoint+"/api/v1/uploads/"+hash, io.LimitReader(f, chunkSize))
		if err != nil {
			return err
		}
		put.ContentLength = chunkSize
		put.Header.Set("Content-Type", "application/octet-stream")
		put.Header.Set("X-RekordLink-Upload-Offset", strconv.FormatInt(offset, 10))
		put.Header.Set("X-RekordLink-Upload-Length", strconv.FormatInt(info.Size(), 10))
		c.authorize(put)
		resp, err = c.http.Do(put)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusConflict {
			err := responseError(resp)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			continue
		}
		if resp.Header.Get("X-RekordLink-Upload-Complete") == "true" {
			log.Printf("uploaded audio %s (%d bytes, resumable)", hash[:12], info.Size())
			return nil
		}
	}
}

func (c *syncClient) uploadBlobLegacy(ctx context.Context, hash string) error {
	f, err := os.Open(c.audio.BlobPath(hash))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, c.state.Endpoint+"/api/v1/blobs/"+hash, f)
	if err != nil {
		return err
	}
	put.ContentLength = info.Size()
	put.Header.Set("Content-Type", "application/octet-stream")
	c.authorize(put)
	resp, err := c.http.Do(put)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return responseError(resp)
	}
	log.Printf("uploaded audio %s (%d bytes)", hash[:12], info.Size())
	return nil
}

func (c *syncClient) downloadBlob(ctx context.Context, hash, destination string) error {
	head, err := http.NewRequestWithContext(ctx, http.MethodHead, c.state.Endpoint+"/api/v1/blobs/"+hash, nil)
	if err != nil {
		return err
	}
	c.authorize(head)
	resp, err := c.http.Do(head)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return responseError(resp)
	}
	total := resp.ContentLength
	resp.Body.Close()
	if total < 0 || total > maxAudioBytes {
		return errors.New("relay returned an invalid audio size")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	part := destination + ".rekordlink-download"
	partInfo, err := os.Stat(part)
	offset := int64(0)
	if err == nil {
		offset = partInfo.Size()
		if !partInfo.Mode().IsRegular() || offset > total {
			if removeErr := os.Remove(part); removeErr != nil {
				return removeErr
			}
			offset = 0
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for offset < total {
		end := min(offset+transferChunkBytes, total) - 1
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.state.Endpoint+"/api/v1/blobs/"+hash, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		req.Header.Set("Accept-Encoding", "identity")
		c.authorize(req)
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK && offset == 0 {
			err := audio.WriteVerified(destination, io.LimitReader(resp.Body, maxAudioBytes+1), hash)
			resp.Body.Close()
			if err != nil {
				return err
			}
			_ = os.Remove(part)
			log.Printf("downloaded audio %s", hash[:12])
			return nil
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return c.downloadBlob(ctx, hash, destination)
		}
		if resp.StatusCode != http.StatusPartialContent {
			err := responseError(resp)
			resp.Body.Close()
			return err
		}
		wanted := end - offset + 1
		file, err := os.OpenFile(part, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			resp.Body.Close()
			return err
		}
		written, copyErr := io.Copy(file, io.LimitReader(resp.Body, wanted+1))
		resp.Body.Close()
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != wanted {
			if written > wanted {
				_ = os.Truncate(part, offset)
			}
			return fmt.Errorf("relay returned %d audio bytes for a %d-byte range", written, wanted)
		}
		offset += written
	}
	if err := audio.CopyVerified(part, destination, hash); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	log.Printf("downloaded audio %s (resumable)", hash[:12])
	return nil
}

func (c *syncClient) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.state.Token)
	req.Header.Set("X-RekordLink-Peer", c.state.PeerID)
}

func responseError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var payload map[string]string
	if json.Unmarshal(b, &payload) == nil && payload["error"] != "" {
		return fmt.Errorf("host returned %s: %s", resp.Status, payload["error"])
	}
	return fmt.Errorf("host returned %s", resp.Status)
}

func publishFile(r *room, peerID, name, path string, audioManager *audio.Manager, exclusions, shared []string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) > maxXMLBytes {
		return errors.New("library XML exceeds the 128 MiB safety limit")
	}
	b, err = preparePublishedSnapshot(b, exclusions, shared)
	if err != nil {
		return err
	}
	if audioManager != nil {
		prepared, err := audioManager.Prepare(b)
		if err != nil {
			return err
		}
		logAudioWarnings(prepared.Warnings)
		b = prepared.XML
	}
	return r.update(peerID, name, b)
}

func watchAndPublish(ctx context.Context, r *room, peerID, name, path string, interval time.Duration, audioManager *audio.Manager, exclusions, shared []string) error {
	initial, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	last := sha256.Sum256(initial)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			b, err := os.ReadFile(path)
			if err != nil {
				log.Printf("watch retry: %v", err)
				continue
			}
			sum := sha256.Sum256(b)
			if sum == last {
				continue
			}
			b, err = preparePublishedSnapshot(b, exclusions, shared)
			if err != nil {
				log.Printf("ignored invalid XML update: %v", err)
				continue
			}
			if audioManager != nil {
				prepared, err := audioManager.Prepare(b)
				if err != nil {
					log.Printf("ignored audio update: %v", err)
					continue
				}
				logAudioWarnings(prepared.Warnings)
				b = prepared.XML
			}
			if err := r.update(peerID, name, b); err != nil {
				log.Printf("ignored invalid XML update: %v", err)
				continue
			}
			last = sum
			log.Printf("published updated host library (%s)", hex.EncodeToString(sum[:6]))
		}
	}
}

func preparePublishedSnapshot(data []byte, exclusions, shared []string) ([]byte, error) {
	data, err := library.ExcludePlaylistsXML(data, exclusions)
	if err != nil {
		return nil, err
	}
	data, err = library.PrepareSharedPlaylistsXML(data, shared)
	if err != nil {
		return nil, err
	}
	clean, removed, err := library.StripManagedPlaylistRootsXML(data)
	if err != nil {
		return nil, err
	}
	if removed > 0 {
		log.Printf("excluded %d imported RekordLink playlist tree(s) from publication", removed)
	}
	return clean, nil
}

func logAudioWarnings(warnings []string) {
	for _, warning := range warnings {
		log.Printf("audio warning: %s", warning)
	}
	if len(warnings) == 100 {
		log.Printf("audio warning: additional unavailable files may have been omitted")
	}
}

func writeCombinedLoop(ctx context.Context, r *room, requesterID, output string, interval time.Duration, audioManager *audio.Manager) error {
	var last [32]byte
	write := func() error {
		b, revision, err := r.combined(requesterID)
		if err != nil {
			return err
		}
		if audioManager != nil {
			b, err = audioManager.Materialize(b, func(hash, destination string) error {
				return audio.CopyVerified(audioManager.BlobPath(hash), destination, hash)
			})
			if err != nil {
				return err
			}
		}
		sum := sha256.Sum256(b)
		if sum == last {
			return nil
		}
		if err := WriteAtomic(output, b, 0o600); err != nil {
			return err
		}
		if err := WriteReceipt(output, b, revision); err != nil {
			return err
		}
		last = sum
		log.Printf("updated merged library: %s", output)
		return nil
	}
	if err := write(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := write(); err != nil {
				log.Printf("merged output retry: %v", err)
			}
		}
	}
}
