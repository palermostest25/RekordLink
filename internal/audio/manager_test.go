package audio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rekordlink/rekordlink/internal/library"
)

func TestPrepareAndMaterialize(t *testing.T) {
	temp := t.TempDir()
	source := filepath.Join(temp, "source", "A & B.mp3")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("synthetic-audio-fixture")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{TrackID: "1", Name: "Test", Location: PathFileURI(source)}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT"}
	xmlData, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(filepath.Join(temp, "managed"), filepath.Join(temp, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := m.Prepare(xmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Hashes) != 1 || !strings.Contains(string(prepared.XML), "rekordlink://sha256/") {
		t.Fatalf("not prepared: hashes=%v xml=%s", prepared.Hashes, prepared.XML)
	}
	materialized, err := m.Materialize(prepared.XML, func(hash, destination string) error {
		t.Fatalf("local blob should be materialized without fetching: %s", hash)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := library.Parse(materialized)
	if err != nil {
		t.Fatal(err)
	}
	path, ok := FileURIPath(final.Collection.Tracks[0].Location)
	if !ok || !strings.Contains(path, filepath.Join(".rekordlink", "library")) {
		t.Fatalf("bad materialized location: %s", final.Collection.Tracks[0].Location)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: %q", got)
	}
	blobInfo, err := os.Stat(m.BlobPath(prepared.Hashes[0]))
	if err != nil {
		t.Fatal(err)
	}
	materializedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(blobInfo, materializedInfo) {
		t.Fatal("expected same-filesystem materialization to use a space-saving hard link")
	}
}

func TestWriteVerifiedRejectsCorruption(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "blob")
	err := WriteVerified(destination, strings.NewReader("wrong"), strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt destination exists: %v", statErr)
	}
}
