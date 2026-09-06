package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rekordlink/rekordlink/internal/audio"
	"github.com/rekordlink/rekordlink/internal/library"
)

func TestReceiptDetectsChangedXMLAndMissingAudio(t *testing.T) {
	temp := t.TempDir()
	audioPath := filepath.Join(temp, "track.mp3")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{TrackID: "1", Name: "Track", Location: audio.PathFileURI(audioPath)}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT"}
	b, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(temp, "shared.xml")
	if err := WriteAtomic(output, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(output, b, `"room-1"`); err != nil {
		t.Fatal(err)
	}
	receipt, err := VerifyReceipt(output)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.LocalAudioPresent != 1 || receipt.LocalAudioMissing != 0 || !strings.Contains(FormatReceipt(receipt), "state: READY") {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	if err := os.Remove(audioPath); err != nil {
		t.Fatal(err)
	}
	receipt, err = VerifyReceipt(output)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.LocalAudioMissing != 1 || !strings.Contains(FormatReceipt(receipt), "state: INCOMPLETE") {
		t.Fatalf("missing audio not detected: %+v", receipt)
	}
	if err := os.WriteFile(output, append(b, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReceipt(output); err == nil {
		t.Fatal("changed XML unexpectedly matched receipt")
	}
}
