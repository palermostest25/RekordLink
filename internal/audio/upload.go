package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrUploadOffset means a resumable uploader used a stale byte offset.
var ErrUploadOffset = errors.New("audio upload offset changed")

// UploadOffset reports the number of durable bytes already received for hash.
// A completed content-addressed blob takes precedence over an abandoned part.
func (m *Manager) UploadOffset(hash string) (offset int64, complete bool, err error) {
	if !ValidHash(hash) {
		return 0, false, errors.New("invalid expected SHA-256")
	}
	m.blobMu.Lock()
	defer m.blobMu.Unlock()
	if info, statErr := os.Stat(m.BlobPath(hash)); statErr == nil && info.Mode().IsRegular() {
		return info.Size(), true, nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return 0, false, statErr
	}
	info, statErr := os.Stat(m.uploadPath(hash))
	if errors.Is(statErr, os.ErrNotExist) {
		return 0, false, nil
	}
	if statErr != nil {
		return 0, false, statErr
	}
	if !info.Mode().IsRegular() {
		return 0, false, errors.New("partial audio upload is not a regular file")
	}
	return info.Size(), false, nil
}

// AppendUpload durably appends one independently retriable request body. Once
// total bytes have arrived, it verifies the complete SHA-256 before publishing
// the immutable blob.
func (m *Manager) AppendUpload(hash string, offset, total int64, reader io.Reader) (int64, bool, error) {
	if !ValidHash(hash) {
		return 0, false, errors.New("invalid expected SHA-256")
	}
	if offset < 0 || total < 0 || offset > total {
		return 0, false, errors.New("invalid upload bounds")
	}
	m.blobMu.Lock()
	defer m.blobMu.Unlock()

	destination := m.BlobPath(hash)
	if info, err := os.Stat(destination); err == nil && info.Mode().IsRegular() {
		return info.Size(), true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, false, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return 0, false, err
	}
	part := m.uploadPath(hash)
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	if info.Size() != offset {
		return info.Size(), false, fmt.Errorf("%w: server has %d bytes, client sent %d", ErrUploadOffset, info.Size(), offset)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, false, err
	}
	written, err := io.Copy(f, reader)
	newOffset := offset + written
	if err != nil {
		_ = f.Sync()
		return newOffset, false, err
	}
	if newOffset > total {
		return newOffset, false, errors.New("audio upload exceeded its declared length")
	}
	if err := f.Sync(); err != nil {
		return newOffset, false, err
	}
	if newOffset != total {
		return newOffset, false, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return newOffset, false, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return newOffset, false, err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != hash {
		_ = f.Close()
		_ = os.Remove(part)
		return 0, false, fmt.Errorf("audio hash mismatch: got %s, expected %s", actual, hash)
	}
	if err := f.Close(); err != nil {
		return newOffset, false, err
	}
	if err := os.Chmod(part, 0o444); err != nil {
		return newOffset, false, err
	}
	if err := os.Rename(part, destination); err != nil {
		return newOffset, false, err
	}
	return newOffset, true, nil
}

func (m *Manager) uploadPath(hash string) string {
	return m.BlobPath(hash) + ".upload"
}
