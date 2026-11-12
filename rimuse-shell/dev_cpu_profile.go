//go:build pprofcpu

package main

// Dev-only CPU profiler (build with -tags pprofcpu): records the HOST
// process's CPU profile to a file for40s from boot so a session-open
// trigger can be correlated with exact Go-side hot functions.
// Excluded from normal builds by the build tag.

import (
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

func init() {
	go func() {
		out := filepath.Join(os.TempDir(), "rimuse-host.cpuprofile")
		f, err := os.Create(out)
		if err != nil {
			return
		}
		_ = pprof.StartCPUProfile(f)
		time.AfterFunc(40*time.Second, func() {
			pprof.StopCPUProfile()
			_ = f.Close()
		})
	}()
}
