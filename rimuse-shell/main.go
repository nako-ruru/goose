package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:skills
var skillsFS embed.FS

// ShellService exposes shell-level capabilities to the frontend.
// Layering rule: this file only does system API adaptation (window creation),
// no business logic.
type ShellService struct {
	app *application.App
}

// OpenStandalone opens a real OS window (the "standalone" display mode) that
// renders the panel URL passed in. The window loads the same SPA with
// ?mode=standalone so the frontend draws the full-bleed panel layout.
func (s *ShellService) OpenStandalone(panelURL string, title string) (uint, error) {
	if s.app == nil {
		return 0, fmt.Errorf("app not ready")
	}
	if title == "" {
		title = "Rimuse Panel"
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            title,
		Width:            1200,
		Height:           800,
		MinWidth:         400,
		MinHeight:        300,
		BackgroundColour: application.NewRGB(17, 17, 20),
		URL:              "/",
	})
	id := w.ID()
	// Rebuild the URL now that the window ID is known so the page can close itself.
	w.SetURL("/?mode=standalone&win=" + fmt.Sprint(id) + "&src=" + url.QueryEscape(panelURL))
	w.Center()
	return id, nil
}

// CloseWindow closes a window by ID (used by the standalone window chrome).
func (s *ShellService) CloseWindow(id uint) error {
	if s.app == nil {
		return fmt.Errorf("app not ready")
	}
	if w, ok := s.app.Window.GetByID(id); ok {
		if closer, ok := w.(interface{ Close() }); ok {
			closer.Close()
			return nil
		}
		return fmt.Errorf("window %d cannot be closed", id)
	}
	return fmt.Errorf("window %d not found", id)
}

// WindowID returns the ID of the window that owns the calling webview,
// so the standalone window can close itself.
func (s *ShellService) WindowID() uint {
	// Fallback: not critical for MVP.
	return 0
}

// GetAcpUrl returns the ACP WebSocket URL of the backend (goose serve).
// Priority: RIMUSE_ACP_URL (dev override, externally managed serve) >
// shell-owned serve (spawned on demand on a free port, respawned on demand
// after crashes — see serve.go).
func (s *ShellService) GetAcpUrl() (string, error) {
	if u := os.Getenv("RIMUSE_ACP_URL"); u != "" {
		return u, nil
	}
	return backend.ensureRunning()
}

// GetSecretKey returns the backend auth secret used by the MCP Apps sandbox
// proxy. External mode uses RIMUSE_SERVE_SECRET; owned mode uses the secret
// generated for this app run (stable across respawns).
func (s *ShellService) GetSecretKey() (string, error) {
	if v := os.Getenv("RIMUSE_SERVE_SECRET"); v != "" {
		return v, nil
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.secret == "" {
		backend.secret = genSecret()
	}
	return backend.secret, nil
}

// GetAppConfig returns startup config consumed by the renderer before the
// first chat (working directory and friends).
func (s *ShellService) GetAppConfig() map[string]any {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		wd, _ = os.UserHomeDir()
	}
	return map[string]any{
		"GOOSE_WORKING_DIR": wd,
	}
}

// installBundledSkills copies the skills embedded in this binary to the
// user's global skills directory (~/.agents/skills), so every machine that
// runs Rimuse gets the bundled guidance without depending on any local path.
// Overwrites on each launch so skill updates ship with app updates.
// Failures are logged, never fatal — the app works without them.
func installBundledSkills() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[skills] cannot resolve home dir: %v", err)
		return
	}
	destRoot := filepath.Join(home, ".agents", "skills")
	entries, err := fs.ReadDir(skillsFS, "skills")
	if err != nil {
		log.Printf("[skills] embedded skills unreadable: %v", err)
		return
	}
	installed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcDir := "skills/" + e.Name()
		dstDir := filepath.Join(destRoot, e.Name())
		err := fs.WalkDir(skillsFS, srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}
			dst := filepath.Join(dstDir, rel)
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			data, err := fs.ReadFile(skillsFS, path)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0o644)
		})
		if err != nil {
			log.Printf("[skills] install %s failed: %v", e.Name(), err)
			continue
		}
		installed++
	}
	if installed > 0 {
		log.Printf("[skills] %d bundled skill(s) installed to %s", installed, destRoot)
	}
}

func main() {
	installBundledSkills()

	shell := &ShellService{}

	app := application.New(application.Options{
		Name:        "rimuse-shell",
		Description: "Rimuse panel shell (MCP Apps display modes on Wails v3)",
		Services: []application.Service{
			application.NewService(shell),
		},
		// Reap the shell-owned goose serve child (taskkill tree) on exit so
		// no orphaned backend survives the app — serve.go item 5.
		OnShutdown: func() {
			stopBackend()
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// DEV ONLY: expose WebView2 CDP for automated screenshots.
		// Wails clears WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS (internal/webview2/webviewloader/env_create.go:169),
		// so the flag must be passed here.
		Windows: application.WindowsOptions{
			// Chromium's native-occlusion tracker wakes the renderer and
			// forces a full repaint the moment the window moves — that was
			// the ~1s "drag start reaction" lag. Disabled via Wails's
			// DisabledFeatures (it builds ONE --disable-features flag; a
			// second one in AdditionalBrowserArgs gets dropped by Chromium).
			DisabledFeatures: []string{
				"msSmartScreenProtection",
				"CalculateNativeWinOcclusion",
			},
			AdditionalBrowserArgs: []string{
				"--remote-debugging-port=9224",
			},
			// Move guard: throttle WebView2's per-frame position notify
			// during drags (see moveguard_windows.go). nil on non-Windows.
			WndProcInterceptor: moveThrottleInterceptor(),
			// UseVisualHosting = false (windowed HWND-child hosting) ON
			// PURPOSE. With visual hosting, every window move pushes the
			// composition position through the WebView2 browser/GPU process —
			// under contention with an active full-screen app on this iGPU,
			// drags trailed (46-130ms per position change measured). Windowed
			// hosting repositions a plain child HWND via USER32 (in-process,
			// contention-insensitive) — the same model Tauri apps use; the
			// user's openidea (Tauri/WebView2) drags smoothly over an active
			// full-screen opencode in the exact repro where ours lagged.
			// The original drag-start lag was traced to native occlusion calc
			// (disabled above), idle churn and orphaned backends — not to
			// windowed hosting, which we had wrongly blamed.
			UseVisualHosting: false,
		},
	})
	shell.app = app

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Rimuse Shell",
		Width:            1280,
		Height:           840,
		MinWidth:         720,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(17, 17, 20),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
