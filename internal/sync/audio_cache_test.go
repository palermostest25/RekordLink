package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/rekordlink/rekordlink/internal/audio"
)

func TestNewClientAudioManagerMigratesLegacyCacheBelowAudioRoot(t *testing.T) {
	temp := t.TempDir()
	stateDir := filepath.Join(temp, "state")
	audioRoot := filepath.Join(temp, "external-audio")
	content := []byte("legacy-client-audio")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	legacyBlob := filepath.Join(stateDir, "blobs", hash[:2], hash)
	if err := os.MkdirAll(filepath.Dir(legacyBlob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyBlob, content, 0o444); err != nil {
		t.Fatal(err)
	}

	manager, err := newClientAudioManager(audioRoot, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	wantBlob := filepath.Join(audio.ManagedBlobDir(audioRoot), hash[:2], hash)
	if manager.BlobPath(hash) != wantBlob {
		t.Fatalf("blob path = %s, want %s", manager.BlobPath(hash), wantBlob)
	}
	if _, err := os.Stat(wantBlob); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "blobs")); !os.IsNotExist(err) {
		t.Fatalf("legacy internal cache remains: %v", err)
	}
}
