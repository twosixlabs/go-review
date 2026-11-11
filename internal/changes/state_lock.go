package changes

import (
	"errors"
	"fmt"
	"os"
)

// Keep the lock file: removing it after unlock lets competing processes lock
// different inodes. The OS owns the lock lifetime, including abrupt process exit.
func acquireStateLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open progress lock (remove obsolete lock directories only when no writer is running): %w", err)
	}
	if err := lockStateFile(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return func() error {
		return errors.Join(unlockStateFile(file), file.Close())
	}, nil
}
