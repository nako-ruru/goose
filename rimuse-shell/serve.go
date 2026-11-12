package main

// Shell-owned goose serve lifecycle — the Wails/Go port of the Electron
// shell's ui/desktop/src/gooseServe.ts (+ loginShellPath.ts, macOS/Linux only).
//
// Design (mirrors upstream behavior):
//  1. binary discovery: GOOSE_BINARY (dev) -> resources/bin next to the exe
//     -> flat layout next to the exe -> PATH lookup; all-miss = clear error
//  2. port: OS-assigned free port on 127.0.0.1 (never touches fixed ports)
//  3. env: inherit ours (Windows PATH comes from the registry automatically),
//     prepend the login-shell PATH on macOS/Linux, inject the secret
//  4. readiness: poll GET /status every 100ms up to 30s; abort early if the
//     child dies; failure carries the stderr tail + log path
//  5. cleanup: taskkill /f /t (process tree) on Windows, Kill elsewhere; idempotent
//  6. crash policy: NO in-place crash loop — the wait-goroutine marks the
//     backend dead; the renderer reconnects, calls GetAcpUrl() again and we
//     respawn on demand (upstream's "open a new chat to start a backend",
//     except ours is fully transparent)
//  7. diagnostics: every phase appended to goose-serve.log under the user
//     cache dir; readiness failures quote the tail
//
// Dev override: RIMUSE_ACP_URL keeps pointing the shell at an externally
// managed serve (the old workflow) and skips spawning entirely.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	serveReadinessTimeout  = 30 * time.Second
	serveReadinessInterval = 100 * time.Millisecond
	serveStatusPath        = "/status"
	serveTailLines         = 40
)

// tailBuffer keeps the last N lines of child output for error reporting.
type tailBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		t.lines = append(t.lines, line)
		if len(t.lines) > serveTailLines {
			t.lines = t.lines[len(t.lines)-serveTailLines:]
		}
	}
	return len(p), nil
}

func (t *tailBuffer) tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

type gooseServeState struct {
	mu      sync.Mutex
	secret  string // generated once per app run (stable across respawns)
	cmd     *exec.Cmd
	port    int
	running bool
	exited  bool
	waitCh  chan struct{} // closed when the current child's Wait returns
	logPath string
}

var backend gooseServeState

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func genSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is unrecoverable; fall back to time+pid noise is
		// not acceptable for a secret — panic is the honest outcome.
		panic(fmt.Sprintf("cannot generate secret: %v", err))
	}
	return hex.EncodeToString(b)
}

func findGooseBinary() (string, error) {
	var candidates []string

	if env := os.Getenv("GOOSE_BINARY"); env != "" {
		// Dev-only override (same policy as upstream: not for packaged builds).
		if st, err := os.Stat(env); err == nil && !st.IsDir() {
			return env, nil
		}
		return "", fmt.Errorf("invalid GOOSE_BINARY path: %s", env)
	}

	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		name := "goose"
		if runtime.GOOS == "windows" {
			name = "goose.exe"
		}
		candidates = append(candidates,
			filepath.Join(dir, "resources", "bin", name), // packaged layout
			filepath.Join(dir, name),                     // flat layout
		)
	}

	if p, err := exec.LookPath("goose"); err == nil {
		candidates = append(candidates, p)
	}

	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("goose binary not found (tried: %s); set GOOSE_BINARY in development",
		strings.Join(candidates, ", "))
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// resolveLoginPATH mirrors upstream loginShellPath.ts: macOS/Linux GUI-launched
// processes miss the PATH their shell profiles add (homebrew/nvm/...), so ask
// the login shell once. Windows inherits the registry PATH — nothing to do.
// Any failure falls back to the current PATH (never fatal).
func resolveLoginPATH() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return resolveLoginPATHUnix()
}

func buildServeEnv(secret, loginPATH string) []string {
	env := os.Environ()
	if loginPATH != "" {
		merged := loginPATH + string(os.PathListSeparator) + os.Getenv("PATH")
		filtered := env[:0]
		for _, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				continue
			}
			filtered = append(filtered, kv)
		}
		env = append(filtered, "PATH="+merged)
	}
	return append(env, "GOOSE_SERVER__SECRET_KEY="+secret)
}

func serveLogPath() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "rimuse", "logs")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "goose-serve.log")
}

// ---------------------------------------------------------------------------
// lifecycle
// ---------------------------------------------------------------------------

func (g *gooseServeState) acpURLLocked() string {
	return fmt.Sprintf("ws://127.0.0.1:%d/acp?token=%s", g.port, g.secret)
}

// ensureRunning returns the ACP URL of a healthy owned backend, (re)spawning
// on demand — this is where crash-recovery happens (item 6).
func (g *gooseServeState) ensureRunning() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.running && !g.exited && g.cmd != nil && g.cmd.Process != nil {
		return g.acpURLLocked(), nil
	}
	return g.startLocked()
}

func (g *gooseServeState) startLocked() (string, error) {
	if g.secret == "" {
		g.secret = genSecret()
	}

	binary, err := findGooseBinary()
	if err != nil {
		return "", err
	}

	port, err := findFreePort()
	if err != nil {
		return "", fmt.Errorf("cannot allocate local port: %w", err)
	}

	args := []string{
		"serve",
		"--platform", "desktop",
		"--enable-scheduler",
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(port),
		"--allowed-origin", "http://wails.localhost",
	}

	logPath := serveLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("cannot open serve log: %w", err)
	}
	fmt.Fprintf(logFile, "\n=== [%s] starting: %s %s\n", time.Now().Format(time.RFC3339), binary, strings.Join(args, " "))
	g.logPath = logPath

	cmd := exec.Command(binary, args...)
	cmd.Env = buildServeEnv(g.secret, resolveLoginPATH())
	cmd.Dir, _ = os.Getwd()
	cmd.SysProcAttr = hideWindowAttr()

	tail := &tailBuffer{}
	// stdout+stderr -> log file; stderr additionally -> tail for diagnostics.
	cmd.Stdout = logFile
	cmd.Stderr = multiWriter{logFile, tail}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("failed to spawn goose serve: %w", err)
	}
	// Ensure the child dies with the shell even on force-kill (no OnShutdown).
	assignChildToKillJob(cmd.Process.Pid)

	waitCh := make(chan struct{})
	g.cmd = cmd
	g.port = port
	g.running = true
	g.exited = false
	g.waitCh = waitCh

	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		close(waitCh)
		g.mu.Lock()
		g.running = false
		g.exited = true
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		g.mu.Unlock()
		log.Printf("[serve] goose serve exited (code=%d): %v", code, err)
		if lf := mustOpenAppend(logPath); lf != nil {
			fmt.Fprintf(lf, "=== [%s] exited code=%d\n", time.Now().Format(time.RFC3339), code)
			_ = lf.Close()
		}
	}()

	// Readiness: poll /status; fail early if the child dies first.
	statusURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, serveStatusPath)
	deadline := time.Now().Add(serveReadinessTimeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-waitCh:
			return "", fmt.Errorf("goose serve exited during startup (see %s)\n%s", logPath, tail.tail())
		default:
		}
		if resp, err := client.Get(statusURL); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Printf("[serve] ready on127.0.0.1:%d (log: %s)", port, logPath)
				fmt.Fprintf(logFile, "=== [%s] ready on 127.0.0.1:%d\n", time.Now().Format(time.RFC3339), port)
				return g.acpURLLocked(), nil
			}
		}
		time.Sleep(serveReadinessInterval)
	}

	// Timeout: kill the child so we never leak a half-started process.
	stopLocked(g)
	return "", fmt.Errorf("goose serve not ready after %s (see %s)\n%s", serveReadinessTimeout, logPath, tail.tail())
}

// stopLocked kills the current child tree. Idempotent.
func stopLocked(g *gooseServeState) {
	if g.cmd == nil || g.cmd.Process == nil || g.exited {
		return
	}
	killProcessTree(g.cmd.Process.Pid)
	// Give the wait goroutine a moment to reap; non-fatal if slow.
	select {
	case <-g.waitCh:
	case <-time.After(3 * time.Second):
	}
}

func stopBackend() {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	stopLocked(&backend)
	log.Print("[serve] backend stopped")
}

// multiWriter without importing io (keeps the file's import block tight).
type multiWriter []interface{ Write(p []byte) (int, error) }

func (m multiWriter) Write(p []byte) (int, error) {
	for _, w := range m {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

func mustOpenAppend(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
