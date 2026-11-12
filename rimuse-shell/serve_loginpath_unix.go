//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// cached login PATH (package-level so the once really caches across calls)
var (
	loginPATHOnce  sync.Once
	loginPATHCache string
)

// resolveLoginPATHUnix asks the user's login+interactive shell for its PATH
// exactly like upstream loginShellPath.ts: GUI-launched processes miss what
// shell profiles add (homebrew/nvm/pyenv/...), and only the launcher knows
// it started from a GUI — the goose CLI itself must keep the PATH it was
// started with, which is why this lives in the shell layer.
//
// Cached once per process run; any failure => "" (caller keeps current PATH).
func resolveLoginPATHUnix() string {
	loginPATHOnce.Do(func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			if sh, err := exec.LookPath("sh"); err == nil {
				shell = sh
			} else {
				return
			}
		}
		// -l -i: login + interactive so profiles actually run; printenv keeps
		// it shell-neutral (echo mangles $PATH under fish).
		cmd := exec.Command(shell, "-l", "-i", "-c", "printenv PATH")
		cmd.Env = os.Environ()
		out := make(chan string, 1)
		if err := cmd.Start(); err != nil {
			return
		}
		go func() {
			buf, _ := cmd.Output()
			out <- strings.TrimSpace(string(buf))
		}()
		select {
		case v := <-out:
			if v != "" {
				// take the last line (profiles may print noise)
				lines := strings.Split(v, "\n")
				loginPATHCache = strings.TrimSpace(lines[len(lines)-1])
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return loginPATHCache
}
