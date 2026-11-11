//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package changes

import (
	"fmt"
	"os"
)

func lockStateFile(*os.File) error {
	return fmt.Errorf("local progress locking is unsupported on this operating system")
}

func unlockStateFile(*os.File) error {
	return fmt.Errorf("local progress locking is unsupported on this operating system")
}
