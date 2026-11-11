package changes

import (
	"fmt"
	"os"
	"syscall"
)

var progressKernel = syscall.NewLazyDLL("kernel32.dll")
var progressLock = progressKernel.NewProc("LockFile")
var progressUnlock = progressKernel.NewProc("UnlockFile")

func lockStateFile(file *os.File) error {
	result, _, err := progressLock.Call(file.Fd(), 0, 0, 1, 0)
	if result == 0 {
		if err == syscall.Errno(33) { // ERROR_LOCK_VIOLATION
			return ErrConflict
		}
		return fmt.Errorf("lock local progress: %w", err)
	}
	return nil
}

func unlockStateFile(file *os.File) error {
	result, _, err := progressUnlock.Call(file.Fd(), 0, 0, 1, 0)
	if result == 0 {
		return fmt.Errorf("unlock local progress: %w", err)
	}
	return nil
}
