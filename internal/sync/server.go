package sync

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rekordlink/rekordlink/internal/audio"
)

const (
	DefaultInterval    = 2 * time.Second
	maxXMLBytes        = 128 << 20
	maxAudioBytes      = int64(8 << 30)
	transferChunkBytes = int64(4 << 20)
)

type HostConfig struct {
	ExcludePlaylists []string
	SharedPlaylists  []string
	LibraryPath      string
	Name             string
	OutputPath       string
	StateDir         string
	ListenAddr       string
	Advertise        string
	Interval         time.Duration
	Version          string
	AudioRoot        string
	ShowInvite       bool
}

type RelayConfig struct {
	StateDir       string
	ListenAddr     string
	Advertise      string
	PublicEndpoint string
	Audio          bool
	Version        string
	ShowInvite     bool
}

type apiServer struct {
	room          *room
	version       string
	ready         atomic.Bool
	registerMu    sync.Mutex
	registerFails map[string][]time.Time
	audio         *audio.Manager
	trustedProxy  bool
	snapshotMu    sync.Mutex
}

type registerRequest struct {
	Name     string `json:"name"`
	PairCode string `json:"pair_code"`
}

type registerResponse struct {
	PeerID string `json:"peer_id"`
	Token  string `json:"token"`
	RoomID string `json:"room_id"`
}

func RunHost(ctx context.Context, cfg HostConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.StateDir, 0o700); err != nil {
		return err
	}
	r, err := newRoom(filepath.Join(cfg.StateDir, "room.json"), cfg.Name)
	if err != nil {
		return err
	}
	hostID := r.hostID()
	var audioManager *audio.Manager
	if cfg.AudioRoot != "" {
		audioManager, err = newClientAudioManager(cfg.AudioRoot, cfg.StateDir)
		if err != nil {
			return err
		}
	}
	if err := publishFile(r, hostID, cfg.Name, cfg.LibraryPath, audioManager, cfg.ExcludePlaylists, cfg.SharedPlaylists); err != nil {
		return fmt.Errorf("initial library: %w", err)
	}
	certPath, keyPath, fingerprint, err := ensureCertificate(cfg.StateDir)
	if err != nil {
		return err
	}
	advertiseHost := cfg.Advertise
	if advertiseHost == "" {
		advertiseHost = localIP()
	}
	_, port, err := net.SplitHostPort(normalizeListen(cfg.ListenAddr))
	if err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	endpoint := "https://" + net.JoinHostPort(advertiseHost, port)
	invite, err := encodeInvite(invitation{Endpoint: endpoint, Fingerprint: fingerprint, PairCode: r.inviteCode(), RoomID: r.diskRoom.RoomID, Audio: audioManager != nil})
	if err != nil {
		return err
	}
	invitePath := filepath.Join(cfg.StateDir, "invite.txt")
	if err := WriteAtomic(invitePath, []byte(invite+"\n"), 0o600); err != nil {
		return fmt.Errorf("write private invite: %w", err)
	}

	server := &apiServer{room: r, version: cfg.Version, registerFails: make(map[string][]time.Time), audio: audioManager}
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}

	errCh := make(chan error, 3)
	go func() {
		log.Printf("room ready for %s", cfg.Name)
		if cfg.ShowInvite {
			log.Printf("share this one-time invite with the other DJ:\n%s", invite)
		} else {
			log.Printf("private invite available at %s", invitePath)
		}
		log.Printf("dashboard: %s", endpoint)
		server.ready.Store(true)
		errCh <- httpServer.ListenAndServeTLS(certPath, keyPath)
	}()
	go func() {
		errCh <- watchAndPublish(ctx, r, hostID, cfg.Name, cfg.LibraryPath, cfg.Interval, audioManager, cfg.ExcludePlaylists, cfg.SharedPlaylists)
	}()
	go func() { errCh <- writeCombinedLoop(ctx, r, hostID, cfg.OutputPath, cfg.Interval, audioManager) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

// RunRelay hosts a symmetric room for two outbound-only DJ clients. Private
// mode terminates pinned TLS itself; public mode expects a trusted HTTPS proxy
// on the advertised endpoint. Relay state is plaintext on its private disk.
func RunRelay(ctx context.Context, cfg RelayConfig) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.StateDir, 0o700); err != nil {
		return err
	}
	r, err := newRelayRoom(filepath.Join(cfg.StateDir, "room.json"))
	if err != nil {
		return err
	}
	var audioManager *audio.Manager
	if cfg.Audio {
		audioManager, err = audio.New(filepath.Join(cfg.StateDir, "materialized"), filepath.Join(cfg.StateDir, "blobs"))
		if err != nil {
			return err
		}
	}
	var certPath, keyPath, endpoint, fingerprint, transport string
	publicEndpoint := strings.TrimSpace(cfg.PublicEndpoint)
	if publicEndpoint != "" {
		if strings.TrimSpace(cfg.Advertise) != "" {
			return errors.New("--public-endpoint and --advertise cannot be used together")
		}
		endpoint, err = ValidatePublicEndpoint(publicEndpoint)
		if err != nil {
			return err
		}
		transport = transportPublicTLS
	} else {
		certPath, keyPath, fingerprint, err = ensureCertificate(cfg.StateDir)
		if err != nil {
			return err
		}
		advertiseHost := cfg.Advertise
		if advertiseHost == "" {
			advertiseHost = localIP()
		}
		_, port, splitErr := net.SplitHostPort(normalizeListen(cfg.ListenAddr))
		if splitErr != nil {
			return fmt.Errorf("listen address: %w", splitErr)
		}
		endpoint = "https://" + net.JoinHostPort(advertiseHost, port)
		transport = transportPinnedTLS
	}
	invite, err := encodeInvite(invitation{Endpoint: endpoint, Fingerprint: fingerprint, Transport: transport, PairCode: r.inviteCode(), RoomID: r.diskRoom.RoomID, Audio: cfg.Audio})
	if err != nil {
		return err
	}
	invitePath := filepath.Join(cfg.StateDir, "invite.txt")
	if err := WriteAtomic(invitePath, []byte(invite+"\n"), 0o600); err != nil {
		return fmt.Errorf("write private invite: %w", err)
	}
	server := &apiServer{room: r, version: cfg.Version, registerFails: make(map[string][]time.Time), audio: audioManager, trustedProxy: publicEndpoint != ""}
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("relay room ready")
		if cfg.ShowInvite {
			log.Printf("send this invite privately to both DJs:\n%s", invite)
		} else {
			log.Printf("private invite available at %s", invitePath)
		}
		log.Printf("dashboard: %s", endpoint)
		server.ready.Store(true)
		if publicEndpoint != "" {
			errCh <- httpServer.ListenAndServe()
		} else {
			errCh <- httpServer.ListenAndServeTLS(certPath, keyPath)
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *apiServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.dashboard)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/v1/register", s.register)
	mux.HandleFunc("POST /api/v1/snapshot", s.auth(s.snapshot))
	mux.HandleFunc("HEAD /api/v1/snapshot-uploads/{hash}", s.auth(s.snapshotUpload))
	mux.HandleFunc("PUT /api/v1/snapshot-uploads/{hash}", s.auth(s.snapshotUpload))
	mux.HandleFunc("GET /api/v1/combined.xml", s.auth(s.combined))
	mux.HandleFunc("GET /api/v1/status", s.auth(s.status))
	mux.HandleFunc("HEAD /api/v1/blobs/{hash}", s.auth(s.blob))
	mux.HandleFunc("GET /api/v1/blobs/{hash}", s.auth(s.blob))
	mux.HandleFunc("PUT /api/v1/blobs/{hash}", s.auth(s.blob))
	mux.HandleFunc("HEAD /api/v1/uploads/{hash}", s.auth(s.upload))
	mux.HandleFunc("PUT /api/v1/uploads/{hash}", s.auth(s.upload))
	return securityHeaders(requestLog(mux))
}

func (s *apiServer) register(w http.ResponseWriter, r *http.Request) {
	remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.trustedProxy {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); forwarded != nil {
			remoteHost = forwarded.String()
		}
	}
	if !s.registrationAllowed(remoteHost, time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many failed pairing attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 80 {
		writeError(w, http.StatusBadRequest, "name must be 1-80 characters")
		return
	}
	peerID, token, err := s.room.register(req.Name, req.PairCode)
	if err != nil {
		s.recordRegistrationFailure(remoteHost, time.Now())
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	s.clearRegistrationFailures(remoteHost)
	writeJSON(w, http.StatusCreated, registerResponse{PeerID: peerID, Token: token, RoomID: s.room.diskRoom.RoomID})
}

func (s *apiServer) registrationAllowed(remote string, now time.Time) bool {
	s.registerMu.Lock()
	defer s.registerMu.Unlock()
	cutoff := now.Add(-time.Minute)
	recent := s.registerFails[remote][:0]
	for _, attempt := range s.registerFails[remote] {
		if attempt.After(cutoff) {
			recent = append(recent, attempt)
		}
	}
	s.registerFails[remote] = recent
	return len(recent) < 5
}

func (s *apiServer) recordRegistrationFailure(remote string, now time.Time) {
	s.registerMu.Lock()
	defer s.registerMu.Unlock()
	s.registerFails[remote] = append(s.registerFails[remote], now)
}

func (s *apiServer) clearRegistrationFailures(remote string) {
	s.registerMu.Lock()
	defer s.registerMu.Unlock()
	delete(s.registerFails, remote)
}

func (s *apiServer) snapshot(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxXMLBytes)
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "library is too large or unreadable")
		return
	}
	if err := s.room.update(r.Header.Get("X-RekordLink-Peer"), r.Header.Get("X-RekordLink-Name"), b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) snapshotUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	hash := r.PathValue("hash")
	if !audio.ValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid snapshot hash")
		return
	}
	peerID := r.Header.Get("X-RekordLink-Peer")
	path := filepath.Join(filepath.Dir(s.room.path), "snapshot-uploads", peerID+"-"+hash+".upload")
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			writeError(w, http.StatusInternalServerError, "snapshot upload state is invalid")
			return
		}
		offset = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "snapshot upload state is unreadable")
		return
	}
	w.Header().Set("X-RekordLink-Upload-Offset", strconv.FormatInt(offset, 10))
	if r.Method == http.MethodHead {
		w.Header().Set("X-RekordLink-Upload-Complete", "false")
		w.WriteHeader(http.StatusOK)
		return
	}
	requestedOffset, err := strconv.ParseInt(r.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
	if err != nil || requestedOffset < 0 {
		writeError(w, http.StatusBadRequest, "invalid snapshot upload offset")
		return
	}
	total, err := strconv.ParseInt(r.Header.Get("X-RekordLink-Upload-Length"), 10, 64)
	if err != nil || total <= 0 || total > maxXMLBytes || requestedOffset > total {
		writeError(w, http.StatusBadRequest, "invalid snapshot upload length")
		return
	}
	if requestedOffset != offset {
		writeError(w, http.StatusConflict, "snapshot upload offset changed")
		return
	}
	if r.ContentLength < 0 || r.ContentLength > transferChunkBytes || requestedOffset+r.ContentLength > total {
		writeError(w, http.StatusRequestEntityTooLarge, "snapshot chunk exceeds the 4 MiB safety limit")
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create snapshot upload state")
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot open snapshot upload state")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, transferChunkBytes)
	defer r.Body.Close()
	written, copyErr := io.Copy(f, r.Body)
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	newOffset := offset + written
	w.Header().Set("X-RekordLink-Upload-Offset", strconv.FormatInt(newOffset, 10))
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusBadRequest, "snapshot chunk was interrupted")
		return
	}
	if newOffset != total {
		w.Header().Set("X-RekordLink-Upload-Complete", "false")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "completed snapshot is unreadable")
		return
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != hash {
		_ = os.Remove(path)
		writeError(w, http.StatusBadRequest, "snapshot hash mismatch")
		return
	}
	if err := s.room.update(peerID, r.Header.Get("X-RekordLink-Name"), data); err != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "cannot clear completed snapshot upload")
		return
	}
	w.Header().Set("X-RekordLink-Upload-Complete", "true")
	w.WriteHeader(http.StatusCreated)
}

func (s *apiServer) combined(w http.ResponseWriter, r *http.Request) {
	b, etag, notModified, err := s.room.combinedIfChanged(r.Header.Get("X-RekordLink-Peer"), r.Header.Get("If-None-Match"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.Header().Set("ETag", etag)
	if notModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="rekordlink-shared.xml"`)
	w.Write(b)
}

func (s *apiServer) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.room.status())
}

func (s *apiServer) blob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if s.audio == nil {
		writeError(w, http.StatusNotFound, "audio synchronization is disabled for this room")
		return
	}
	hash := r.PathValue("hash")
	if !audio.ValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid audio hash")
		return
	}
	path := s.audio.BlobPath(hash)
	switch r.Method {
	case http.MethodHead:
		if size, ok := s.audio.HasBlob(hash); ok {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
			w.Header().Set("ETag", `"sha256-`+hash+`"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case http.MethodGet:
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, "audio blob not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "audio blob is unreadable")
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "audio blob is unreadable")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"sha256-`+hash+`"`)
		http.ServeContent(w, r, hash, info.ModTime(), f)
	case http.MethodPut:
		if _, ok := s.audio.HasBlob(hash); ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.ContentLength < 0 || r.ContentLength > maxAudioBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "audio file size is missing or exceeds 8 GiB")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAudioBytes)
		defer r.Body.Close()
		if err := audio.WriteVerified(path, r.Body, hash); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *apiServer) upload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if s.audio == nil {
		writeError(w, http.StatusNotFound, "audio synchronization is disabled for this room")
		return
	}
	hash := r.PathValue("hash")
	if !audio.ValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid audio hash")
		return
	}
	if r.Method == http.MethodHead {
		offset, complete, err := s.audio.UploadOffset(hash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "audio upload state is unreadable")
			return
		}
		w.Header().Set("X-RekordLink-Upload-Offset", strconv.FormatInt(offset, 10))
		w.Header().Set("X-RekordLink-Upload-Complete", strconv.FormatBool(complete))
		w.WriteHeader(http.StatusOK)
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("X-RekordLink-Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid upload offset")
		return
	}
	total, err := strconv.ParseInt(r.Header.Get("X-RekordLink-Upload-Length"), 10, 64)
	if err != nil || total < 0 || total > maxAudioBytes || offset > total {
		writeError(w, http.StatusBadRequest, "invalid upload length")
		return
	}
	if r.ContentLength < 0 || r.ContentLength > transferChunkBytes || offset+r.ContentLength > total {
		writeError(w, http.StatusRequestEntityTooLarge, "audio chunk exceeds the 4 MiB safety limit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, transferChunkBytes)
	defer r.Body.Close()
	newOffset, complete, err := s.audio.AppendUpload(hash, offset, total, r.Body)
	w.Header().Set("X-RekordLink-Upload-Offset", strconv.FormatInt(newOffset, 10))
	w.Header().Set("X-RekordLink-Upload-Complete", strconv.FormatBool(complete))
	if err != nil {
		if errors.Is(err, audio.ErrUploadOffset) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if complete {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *apiServer) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": s.ready.Load(), "version": s.version, "shared_playlists": true})
}

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>RekordLink</title><style>
:root{color-scheme:dark;font-family:ui-sans-serif,system-ui,sans-serif;background:#0a0c12;color:#f4f6fb}body{max-width:780px;margin:10vh auto;padding:24px}.card{background:#141824;border:1px solid #2a3143;border-radius:18px;padding:28px;box-shadow:0 24px 80px #0008}h1{font-size:42px;margin:0 0 8px;color:#71e4c3}.muted{color:#aab3c8}.pill{display:inline-block;background:#1e3e36;color:#92f7d9;padding:6px 10px;border-radius:999px;font-weight:700}code{display:block;background:#090b10;padding:14px;border-radius:10px;overflow:auto}li{margin:.7em 0}</style></head>
<body><main class="card"><span class="pill">Room online</span><h1>RekordLink</h1><p class="muted">Secure two-DJ library linking.</p>
<ol><li>Keep rekordbox exporting to your watched XML file.</li><li>Leave RekordLink running on both computers.</li><li>In rekordbox, select <code>rekordlink-shared.xml</code> as the imported Bridge library.</li></ol>
<p class="muted">Library contents require the private paired client token and are not exposed on this page.</p></main></body></html>`))

func (s *apiServer) dashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dashboardTemplate.Execute(w, nil)
}

func (s *apiServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		peerID := r.Header.Get("X-RekordLink-Peer")
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if peerID == "" || token == "" || !s.room.authenticate(peerID, token) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			log.Printf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

func normalizeListen(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

// ValidatePublicEndpoint accepts only a bare HTTPS origin. Public relay mode
// relies on the operating system trust store for this address.
func ValidatePublicEndpoint(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("public endpoint must be an HTTPS origin such as https://rekordlink.example.com")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func localIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			return addr.IP.String()
		}
	}
	return "127.0.0.1"
}
