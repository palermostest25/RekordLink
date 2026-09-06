package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/rekordlink/rekordlink/internal/library"
)

const blobScheme = "rekordlink"

var supportedExtensions = map[string]bool{
	".aac": true, ".aif": true, ".aiff": true, ".alac": true,
	".flac": true, ".m4a": true, ".mp3": true, ".ogg": true,
	".opus": true, ".wav": true,
}

type Manager struct {
	root      string
	blobDir   string
	blobMu    sync.Mutex
	indexMu   sync.Mutex
	indexPath string
	index     map[string]indexEntry
}

type indexEntry struct {
	Size        int64  `json:"size"`
	ModifiedNS  int64  `json:"modified_ns"`
	ContentHash string `json:"content_hash"`
}

type Prepared struct {
	XML      []byte
	Hashes   []string
	Warnings []string
}

func New(root, blobDir string) (*Manager, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(blobDir) == "" {
		return nil, errors.New("audio root and blob directory are required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absBlobs, err := filepath.Abs(blobDir)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{absRoot, absBlobs, filepath.Join(absRoot, ".rekordlink", "library")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	m := &Manager{root: absRoot, blobDir: absBlobs, indexPath: filepath.Join(absBlobs, "index.json"), index: make(map[string]indexEntry)}
	if b, err := os.ReadFile(m.indexPath); err == nil {
		if err := json.Unmarshal(b, &m.index); err != nil {
			return nil, fmt.Errorf("read audio index: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m, nil
}

// Prepare copies every supported local audio file referenced by the XML into
// the private blob cache, then replaces its Location with a content-addressed
// URI. The caller uploads all returned hashes before publishing the XML.
func (m *Manager) Prepare(data []byte) (Prepared, error) {
	lib, err := library.Parse(data)
	if err != nil {
		return Prepared{}, err
	}
	unique := make(map[string]bool)
	var warnings []string
	indexChanged := false
	for i := range lib.Collection.Tracks {
		track := &lib.Collection.Tracks[i]
		path, ok := FileURIPath(track.Location)
		if !ok || !supportedExtensions[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			if len(warnings) < 100 {
				reason := "not a regular file"
				if err != nil {
					reason = err.Error()
				}
				warnings = append(warnings, fmt.Sprintf("audio unavailable for %q: %s", displayName(*track), reason))
			}
			continue
		}
		hash, cached := m.cachedHash(path, info)
		if !cached {
			hash, err = HashFile(path)
			if err != nil {
				if len(warnings) < 100 {
					warnings = append(warnings, fmt.Sprintf("audio unavailable for %q: %s", displayName(*track), err))
				}
				continue
			}
			m.rememberHash(path, info, hash)
			indexChanged = true
		}
		if err := m.StoreFile(hash, path); err != nil {
			return Prepared{}, err
		}
		track.Location = BlobURI(hash, filepath.Base(path))
		unique[hash] = true
	}
	out, err := library.Marshal(lib)
	if err != nil {
		return Prepared{}, err
	}
	hashes := make([]string, 0, len(unique))
	for hash := range unique {
		hashes = append(hashes, hash)
	}
	if indexChanged {
		if err := m.saveIndex(); err != nil {
			return Prepared{}, err
		}
	}
	return Prepared{XML: out, Hashes: hashes, Warnings: warnings}, nil
}

// Materialize ensures every content-addressed Location exists in the managed
// local library and rewrites it to a normal file://localhost URI.
func (m *Manager) Materialize(data []byte, fetch func(hash, destination string) error) ([]byte, error) {
	lib, err := library.Parse(data)
	if err != nil {
		return nil, err
	}
	for i := range lib.Collection.Tracks {
		track := &lib.Collection.Tracks[i]
		hash, name, ok := ParseBlobURI(track.Location)
		if !ok {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !supportedExtensions[ext] {
			ext = ".audio"
		}
		destination := filepath.Join(m.root, ".rekordlink", "library", hash[:2], hash+ext)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, err
		}
		if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
			if _, local := m.HasBlob(hash); local {
				err = LinkOrCopyVerified(m.BlobPath(hash), destination, hash)
			} else {
				err = fetch(hash, destination)
			}
			if err != nil {
				return nil, fmt.Errorf("fetch audio for %q: %w", displayName(*track), err)
			}
		} else if err != nil {
			return nil, err
		}
		track.Location = PathFileURI(destination)
	}
	return library.Marshal(lib)
}

func (m *Manager) StoreFile(hash, source string) error {
	m.blobMu.Lock()
	defer m.blobMu.Unlock()
	destination := m.BlobPath(hash)
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return CopyVerified(source, destination, hash)
}

func (m *Manager) cachedHash(path string, info os.FileInfo) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	m.indexMu.Lock()
	entry, ok := m.index[abs]
	m.indexMu.Unlock()
	if !ok || entry.Size != info.Size() || entry.ModifiedNS != info.ModTime().UnixNano() || !ValidHash(entry.ContentHash) {
		return "", false
	}
	if _, exists := m.HasBlob(entry.ContentHash); !exists {
		return "", false
	}
	return entry.ContentHash, true
}

func (m *Manager) rememberHash(path string, info os.FileInfo, hash string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	m.indexMu.Lock()
	m.index[abs] = indexEntry{Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), ContentHash: hash}
	m.indexMu.Unlock()
}

func (m *Manager) saveIndex() error {
	m.indexMu.Lock()
	b, err := json.MarshalIndent(m.index, "", "  ")
	m.indexMu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.indexPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.indexPath), ".rekordlink-index-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, m.indexPath)
}

func (m *Manager) BlobPath(hash string) string {
	return filepath.Join(m.blobDir, hash[:2], hash)
}

func (m *Manager) HasBlob(hash string) (int64, bool) {
	if !ValidHash(hash) {
		return 0, false
	}
	info, err := os.Stat(m.BlobPath(hash))
	return func() int64 {
		if err == nil {
			return info.Size()
		}
		return 0
	}(), err == nil && info.Mode().IsRegular()
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func CopyVerified(source, destination, expectedHash string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	return WriteVerified(destination, src, expectedHash)
}

// LinkOrCopyVerified materializes an immutable cached blob without consuming a
// second file's worth of space when source and destination share a filesystem.
// Filesystems that do not support hard links transparently fall back to a
// verified copy.
func LinkOrCopyVerified(source, destination, expectedHash string) error {
	if !ValidHash(expectedHash) {
		return errors.New("invalid expected SHA-256")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	return CopyVerified(source, destination, expectedHash)
}

func WriteVerified(destination string, reader io.Reader, expectedHash string) error {
	if !ValidHash(expectedHash) {
		return errors.New("invalid expected SHA-256")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".rekordlink-audio-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), reader); err != nil {
		tmp.Close()
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHash {
		tmp.Close()
		return fmt.Errorf("audio hash mismatch: got %s, expected %s", actual, expectedHash)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o444); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, destination)
}

func BlobURI(hash, name string) string {
	u := url.URL{Scheme: blobScheme, Host: "sha256", Path: "/" + hash}
	q := u.Query()
	q.Set("name", filepath.Base(name))
	u.RawQuery = q.Encode()
	return u.String()
}

func ParseBlobURI(value string) (hash, name string, ok bool) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != blobScheme || u.Host != "sha256" {
		return "", "", false
	}
	hash = strings.TrimPrefix(u.Path, "/")
	if !ValidHash(hash) {
		return "", "", false
	}
	name = filepath.Base(u.Query().Get("name"))
	if name == "." || name == "" {
		name = hash + ".audio"
	}
	return hash, name, true
}

func ValidHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func FileURIPath(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
		return "", false
	}
	path := filepath.FromSlash(u.Path)
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == filepath.Separator && path[2] == ':' {
		path = path[1:]
	}
	return path, filepath.IsAbs(path)
}

func PathFileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	slash := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Host: "localhost", Path: slash}).String()
}

func displayName(track library.Track) string {
	if track.Artist != "" {
		return track.Artist + " - " + track.Name
	}
	return track.Name
}
