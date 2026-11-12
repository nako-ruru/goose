// ShellService commands 鈥?the Tauri counterpart of the Wails ShellService
// (rimuse-shell/main.go, frozen at tag `wails-baseline`).
//
// P1 surface: the three methods the frontend actually calls through
// src/bridge/shellService.ts. Serve lifecycle (shell-owned backend) moves
// here in P2; window commands (OpenStandalone/CloseWindow) in P3.

use std::collections::HashMap;
use std::env;

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
