//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package changes

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockStateFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock local progress: %w", err)
	}
	return nil
}

func unlockStateFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock local progress: %w", err)
	}
	return nil
}
