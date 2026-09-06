package audio

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateBlobCacheMovesVerifiedObjectsAndRemovesLegacy(t *testing.T) {
	temp := t.TempDir()
	legacy := filepath.Join(temp, "state", "blobs")
	managedRoot := filepath.Join(temp, "managed")
	managed := ManagedBlobDir(managedRoot)
	content := []byte("cached-audio")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	legacyBlob := filepath.Join(legacy, hash[:2], hash)
	if err := os.MkdirAll(filepath.Dir(legacyBlob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyBlob, content, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "index.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyBlob+".upload", []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateBlobCache(legacy, managed)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy cache still exists: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(managed, hash[:2], hash))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("migrated content = %q", got)
	}
}

func TestMigrateBlobCacheRejectsCorruptObjectWithoutRemovingLegacy(t *testing.T) {
	temp := t.TempDir()
	legacy := filepath.Join(temp, "legacy")
	hash := strings.Repeat("0", 64)
	legacyBlob := filepath.Join(legacy, hash[:2], hash)
	if err := os.MkdirAll(filepath.Dir(legacyBlob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyBlob, []byte("not-the-named-hash"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateBlobCache(legacy, filepath.Join(temp, "managed")); err == nil {
		t.Fatal("expected corrupt cache error")
	}
	if _, err := os.Stat(legacyBlob); err != nil {
		t.Fatalf("legacy object was removed after failed migration: %v", err)
	}
}

func TestMigrateBlobCacheRejectsNestedDestination(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateBlobCache(legacy, filepath.Join(legacy, "nested")); err == nil {
		t.Fatal("expected nested destination error")
	}
}
