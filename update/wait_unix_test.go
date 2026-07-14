//go:build darwin || linux

package update

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWaitForPIDWaitsForRealProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 0.15")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	start := time.Now()
	if err := WaitForPID(cmd.Process.Pid, time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("returned before process exited: %s", elapsed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWaitForPIDTimesOut(t *testing.T) {
	err := WaitForPID(os.Getpid(), 50*time.Millisecond)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("error = %v, want ErrWaitTimeout", err)
	}
}
