package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rekordlink/rekordlink/internal/audio"
	"github.com/rekordlink/rekordlink/internal/library"
)

type Receipt struct {
	Schema               int       `json:"schema"`
	GeneratedAt          time.Time `json:"generated_at"`
	RoomRevision         string    `json:"room_revision"`
	XMLSHA256            string    `json:"xml_sha256"`
	Tracks               int       `json:"tracks"`
	Playlists            int       `json:"playlists"`
	LocalAudioReferences int       `json:"local_audio_references"`
	LocalAudioPresent    int       `json:"local_audio_present"`
	LocalAudioMissing    int       `json:"local_audio_missing"`
}

func WriteReceipt(output string, data []byte, revision string) error {
	receipt, err := inspectOutput(data, revision)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(output+".status.json", append(b, '\n'), 0o600)
}

func VerifyReceipt(output string) (Receipt, error) {
	statusBytes, err := os.ReadFile(output + ".status.json")
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := json.Unmarshal(statusBytes, &receipt); err != nil {
		return Receipt{}, err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return Receipt{}, err
	}
	actual, err := inspectOutput(data, receipt.RoomRevision)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.Schema != 1 || receipt.XMLSHA256 != actual.XMLSHA256 {
		return Receipt{}, errors.New("sync receipt does not match the current merged XML")
	}
	// Files can be removed after publication, so refresh availability rather
	// than trusting the recorded count.
	receipt.LocalAudioReferences = actual.LocalAudioReferences
	receipt.LocalAudioPresent = actual.LocalAudioPresent
	receipt.LocalAudioMissing = actual.LocalAudioMissing
	return receipt, nil
}

func inspectOutput(data []byte, revision string) (Receipt, error) {
	lib, err := library.Parse(data)
	if err != nil {
		return Receipt{}, err
	}
	sum := sha256.Sum256(data)
	receipt := Receipt{
		Schema:       1,
		GeneratedAt:  time.Now().UTC(),
		RoomRevision: revision,
		XMLSHA256:    hex.EncodeToString(sum[:]),
		Tracks:       len(lib.Collection.Tracks),
		Playlists:    library.PlaylistCount(lib.Playlists.Root),
	}
	for _, track := range lib.Collection.Tracks {
		path, ok := audio.FileURIPath(track.Location)
		if !ok || !isAudioPath(path) {
			continue
		}
		receipt.LocalAudioReferences++
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			receipt.LocalAudioPresent++
		} else {
			receipt.LocalAudioMissing++
		}
	}
	return receipt, nil
}

func isAudioPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".audio", ".flac", ".m4a", ".mp3", ".ogg", ".opus", ".wav":
		return true
	default:
		return false
	}
}

func FormatReceipt(receipt Receipt) string {
	state := "READY"
	if receipt.LocalAudioMissing > 0 {
		state = "INCOMPLETE"
	}
	return fmt.Sprintf("state: %s\ngenerated: %s\nroom revision: %s\ntracks: %d\nplaylists: %d\nlocal audio: %d/%d present\nxml sha256: %s\n",
		state, receipt.GeneratedAt.Format(time.RFC3339), receipt.RoomRevision, receipt.Tracks, receipt.Playlists,
		receipt.LocalAudioPresent, receipt.LocalAudioReferences, receipt.XMLSHA256)
}
