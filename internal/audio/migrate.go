package audio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ManagedBlobDir returns the bulk cache location below the operator-selected
// managed audio root. Keeping blobs here prevents a large library from filling
// the operating-system/configuration disk when the user selected another disk.
func ManagedBlobDir(audioRoot string) string {
	return filepath.Join(audioRoot, ".rekordlink", "blobs")
}

// MigrateBlobCache safely moves the legacy state-directory cache into the
// managed audio root. Complete content-addressed objects are hash-verified
// before the legacy directory is removed. A same-filesystem migration is an
// atomic rename; cross-filesystem migration copies verified completed objects
// and lets partial transfers restart from the authoritative peer.
func MigrateBlobCache(legacyDir, managedDir string) (bool, error) {
	legacyAbs, err := filepath.Abs(legacyDir)
	if err != nil {
		return false, err
	}
	managedAbs, err := filepath.Abs(managedDir)
	if err != nil {
		return false, err
	}
	if filepath.Clean(legacyAbs) == filepath.Clean(managedAbs) {
		return false, nil
	}
	if relative, relErr := filepath.Rel(legacyAbs, managedAbs); relErr == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, errors.New("managed audio cache must not be inside the legacy cache")
	}
	info, err := os.Stat(legacyAbs)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("legacy audio cache is not a directory: %s", legacyAbs)
	}
	if err := validateBlobCache(legacyAbs); err != nil {
		return false, fmt.Errorf("validate legacy audio cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(managedAbs), 0o700); err != nil {
		return false, fmt.Errorf("create managed audio cache: %w", err)
	}
	if _, statErr := os.Stat(managedAbs); errors.Is(statErr, os.ErrNotExist) {
		if renameErr := os.Rename(legacyAbs, managedAbs); renameErr == nil {
			return true, nil
		}
	}
	if err := os.MkdirAll(managedAbs, 0o700); err != nil {
		return false, fmt.Errorf("create managed audio cache: %w", err)
	}

	err = filepath.WalkDir(legacyAbs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("legacy audio cache contains a symbolic link: %s", path)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("legacy audio cache contains a non-regular file: %s", path)
		}
		hash := filepath.Base(path)
		if !ValidHash(hash) {
			return nil
		}
		actual, err := HashFile(path)
		if err != nil {
			return err
		}
		if actual != hash {
			return fmt.Errorf("legacy audio cache object %s failed SHA-256 verification", path)
		}
		destination := filepath.Join(managedAbs, hash[:2], hash)
		if _, err := os.Stat(destination); err == nil {
			destinationHash, hashErr := HashFile(destination)
			if hashErr != nil {
				return hashErr
			}
			if destinationHash != hash {
				return fmt.Errorf("managed audio cache object %s failed SHA-256 verification", destination)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return CopyVerified(path, destination, hash)
	})
	if err != nil {
		return false, fmt.Errorf("migrate audio cache: %w", err)
	}
	if err := os.RemoveAll(legacyAbs); err != nil {
		return false, fmt.Errorf("remove migrated legacy audio cache: %w", err)
	}
	return true, nil
}

func validateBlobCache(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("audio cache contains a symbolic link: %s", path)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("audio cache contains a non-regular file: %s", path)
		}
		hash := filepath.Base(path)
		if !ValidHash(hash) {
			return nil
		}
		actual, err := HashFile(path)
		if err != nil {
			return err
		}
		if actual != hash {
			return fmt.Errorf("audio cache object %s failed SHA-256 verification", path)
		}
		return nil
	})
}
