//go:build unix

package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rekordlink/rekordlink/internal/library"
)

func TestPrepareSkipsUnreadableSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "protected.mp3")
	if err := os.WriteFile(source, []byte("protected audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(source, 0o600)
	if f, err := os.Open(source); err == nil {
		f.Close()
		t.Skip("test user can read mode-000 files")
	}

	manager, err := New(filepath.Join(root, "managed"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{TrackID: "1", Name: "Protected", Location: PathFileURI(source)}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT"}
	data, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Prepare(data)
	if err != nil {
		t.Fatalf("unreadable source aborted preparation: %v", err)
	}
	if len(prepared.Warnings) != 1 || len(prepared.Hashes) != 0 {
		t.Fatalf("unexpected result: warnings=%v hashes=%v", prepared.Warnings, prepared.Hashes)
	}
	parsed, err := library.Parse(prepared.XML)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Collection.Tracks[0].Location != PathFileURI(source) {
		t.Fatal("unreadable track location was unexpectedly rewritten")
	}
}
