//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package review

import (
	"os/exec"
	"time"
)

func boundReviewProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = time.Second
}
