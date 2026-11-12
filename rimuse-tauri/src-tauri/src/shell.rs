// ShellService commands — the Tauri counterpart of the Wails ShellService
// (rimuse-shell/main.go, frozen at tag `wails-baseline`).
//
// Surface: the three methods the frontend calls through
// src/bridge/shellService.ts (get_acp_url/get_secret_key/get_app_config)
// plus the window trio (open_standalone/close_window/window_id) ported in
// P3 for capability parity — no frontend caller yet, same shape both sides.
// Serve lifecycle lives in serve.rs (P2); skills install in skills.rs.

use std::collections::HashMap;
use std::env;
use std::sync::atomic::{AtomicU32, Ordering};

use tauri::Manager;

/// Monotonic id source for standalone windows. 0 is reserved for
/// "main/unknown" (the Wails WindowID fallback convention).
static NEXT_WINDOW_ID: AtomicU32 = AtomicU32::new(1);

/// MUST stay byte-identical to tauri.conf.json app.windows[0].additionalBrowserArgs.
///
/// WebView2 requires every window sharing one user data folder to be created
/// with *identical* AdditionalBrowserArguments; a mismatch makes the second
/// window fail with 0x8007139F (ERROR_INVALID_STATE) because the browser
/// process is already running and cannot re-apply switches (tauri#8196).
/// wry's default (`--disable-features=msWebOOUI,...`) differs from our
/// configured CDP arg, so every window we create at runtime must repeat it.
/// The browser process is reused, so the debug port is not bound twice.
const BROWSER_ARGS: &str = "--remote-debugging-port=9224";

/// ACP WebSocket URL of the backend goose serve.
/// Priority: RIMUSE_ACP_URL (dev override, externally managed serve) >
/// shell-owned serve (spawned on demand on a free port, respawned on
/// demand after crashes — see serve.rs, P2 parity with Wails GetAcpUrl).
#[tauri::command]
pub fn get_acp_url() -> Result<String, String> {
    if let Ok(u) = env::var("RIMUSE_ACP_URL") {
        if !u.is_empty() {
            return Ok(u);
        }
    }
    crate::serve::ensure_running().map(|(url, _)| url)
}

/// Backend auth secret for the MCP Apps sandbox proxy.
/// External mode uses RIMUSE_SERVE_SECRET; owned mode uses the per-run
/// generated secret (stable across respawns).
#[tauri::command]
pub fn get_secret_key() -> String {
    if let Ok(v) = env::var("RIMUSE_SERVE_SECRET") {
        if !v.is_empty() {
            return v;
        }
    }
    crate::serve::managed_secret()
}

/// Startup config hydrated into the renderer before the first chat.
#[tauri::command]
pub fn get_app_config() -> HashMap<String, String> {
    let mut cfg = HashMap::new();
    match env::current_dir() {
        Ok(wd) => {
            cfg.insert("GOOSE_WORKING_DIR".to_string(), wd.to_string_lossy().into_owned());
        }
        Err(_) => {
            if let Ok(home) = env::var("USERPROFILE").or_else(|_| env::var("HOME")) {
                cfg.insert("GOOSE_WORKING_DIR".to_string(), home);
            }
        }
    }
    cfg
}

/// OpenStandalone opens a real OS window rendering the SPA in standalone
/// panel mode (parity with Wails ShellService.OpenStandalone): 1200x800,
/// min 400x300, dark background, centered; the URL carries
/// ?mode=standalone&win=<id>&src=<panelURL> so the page can close itself.
///
/// MUST be `async fn`: tauri-runtime-wry's `create_window` dispatches a
/// message to the event loop and blocks on a channel ("must be called from
/// a separate thread, otherwise the channel will introduce a deadlock").
/// A sync command runs inline on the main thread — the one dispatching the
/// IPC — so it would wait for itself: the window never appears AND the main
/// thread wedges, killing every subsequent invoke. async moves execution to
/// a tokio worker thread, which is what `create_window` requires.
#[tauri::command]
pub async fn open_standalone(
    app: tauri::AppHandle,
    panel_url: String,
    title: String,
) -> Result<u32, String> {
    let id = NEXT_WINDOW_ID.fetch_add(1, Ordering::Relaxed);
    let label = format!("standalone-{id}");
    let title = if title.is_empty() {
        "Rimuse Panel".to_string()
    } else {
        title
    };
    let url = format!("/?mode=standalone&win={id}&src={}", query_escape(&panel_url));
    tauri::WebviewWindowBuilder::new(&app, label, tauri::WebviewUrl::App(url.into()))
        .title(title)
        .inner_size(1200.0, 800.0)
        .min_inner_size(400.0, 300.0)
        .background_color(tauri::window::Color(17, 17, 20, 255))
        .additional_browser_args(BROWSER_ARGS)
        .center()
        .build()
        .map_err(|e| format!("create standalone window: {e}"))?;
    Ok(id)
}

/// CloseWindow closes a standalone window by the id returned from
/// open_standalone (parity with Wails ShellService.CloseWindow).
/// async for the same thread-safety reason as `open_standalone`.
#[tauri::command]
pub async fn close_window(app: tauri::AppHandle, id: u32) -> Result<(), String> {
    let label = format!("standalone-{id}");
    match app.get_webview_window(&label) {
        Some(w) => w.close().map_err(|e| format!("close window {id}: {e}")),
        None => Err(format!("window {id} not found")),
    }
}

/// WindowID returns the id of the calling window (0 = main/unknown).
/// The Wails baseline returns 0 unconditionally (documented MVP fallback);
/// Tauri fulfills the documented intent instead: standalone windows report
/// their real id, the main window keeps the 0 fallback.
#[tauri::command]
pub fn window_id(window: tauri::WebviewWindow) -> u32 {
    window
        .label()
        .strip_prefix("standalone-")
        .and_then(|s| s.parse().ok())
        .unwrap_or(0)
}

/// Percent-encode a query component like Go's url.QueryEscape (the Wails
/// baseline's encoder): unreserved A-Za-z0-9-._~ pass through, space -> '+',
/// everything else becomes uppercase %XX.
fn query_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char);
            }
            b' ' => out.push('+'),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}
