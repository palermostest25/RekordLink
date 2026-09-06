package sync

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/rekordlink/rekordlink/internal/audio"
)

func newClientAudioManager(audioRoot, stateDir string) (*audio.Manager, error) {
	managedBlobs := audio.ManagedBlobDir(audioRoot)
	legacyBlobs := filepath.Join(stateDir, "blobs")
	migrated, err := audio.MigrateBlobCache(legacyBlobs, managedBlobs)
	if err != nil {
		return nil, fmt.Errorf("move audio cache to managed audio folder: %w", err)
	}
	if migrated {
		log.Printf("moved cached audio from %s to %s", legacyBlobs, managedBlobs)
	}
	return audio.New(audioRoot, managedBlobs)
}
