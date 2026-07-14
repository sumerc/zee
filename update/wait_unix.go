//go:build darwin || linux

package update

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

var ErrWaitTimeout = errors.New("timed out waiting for process to exit")

func WaitForPID(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("check pid %d: %w", pid, err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("%w: pid %d", ErrWaitTimeout, pid)
		}
	}
}
