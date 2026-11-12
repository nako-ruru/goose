//go:build !windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

func hideWindowAttr() *syscall.SysProcAttr {
	// No window to hide on unix; child in its own process group would let us
	// kill the whole tree — kept simple for now (single-level goose process).
	return nil
}

func killProcessTree(pid int) {
	// Best effort: kill the group if we ever set Setpgid (currently nil attr
	// means no group), otherwise the direct child — the wait goroutine reaps it.
	_ = exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Signal(syscall.SIGKILL)
	}
}

// assignChildToKillJob is a Windows-only safety net; unix shells rely on
// graceful shutdown (OnShutdown) here for now.
func assignChildToKillJob(pid int) {}
