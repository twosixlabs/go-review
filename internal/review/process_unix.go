//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package review

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func boundReviewProcess(cmd *exec.Cmd) {
	// A verification script owns its descendants. Cancellation must not leave
	// its test runners or services running after the review request is gone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
}
