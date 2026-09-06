package audio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResumableUpload(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "managed"), filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("rekordlink-audio"), 1024)
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	offset, complete, err := manager.AppendUpload(hash, 0, int64(len(content)), bytes.NewReader(content[:4096]))
	if err != nil || complete || offset != 4096 {
		t.Fatalf("first chunk: offset=%d complete=%v err=%v", offset, complete, err)
	}
	gotOffset, complete, err := manager.UploadOffset(hash)
	if err != nil || complete || gotOffset != offset {
		t.Fatalf("resume state: offset=%d complete=%v err=%v", gotOffset, complete, err)
	}
	if _, _, err := manager.AppendUpload(hash, 0, int64(len(content)), bytes.NewReader(nil)); !errors.Is(err, ErrUploadOffset) {
		t.Fatalf("expected offset conflict, got %v", err)
	}
	offset, complete, err = manager.AppendUpload(hash, offset, int64(len(content)), bytes.NewReader(content[offset:]))
	if err != nil || !complete || offset != int64(len(content)) {
		t.Fatalf("final chunk: offset=%d complete=%v err=%v", offset, complete, err)
	}
	stored, err := os.ReadFile(manager.BlobPath(hash))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatal("completed upload content differs")
	}
}
