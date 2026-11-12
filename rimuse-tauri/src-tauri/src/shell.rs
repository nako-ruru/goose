// ShellService commands 鈥?the Tauri counterpart of the Wails ShellService
// (rimuse-shell/main.go, frozen at tag `wails-baseline`).
//
// P1 surface: the three methods the frontend actually calls through
// src/bridge/shellService.ts. Serve lifecycle (shell-owned backend) moves
// here in P2; window commands (OpenStandalone/CloseWindow) in P3.

use std::collections::HashMap;
use std::env;

/// ACP WebSocket URL of the backend goose serve.
/// P1: external backend via RIMUSE_ACP_URL (dev parity with the Wails shell);
/// P2 replaces the error branch with the shell-owned spawn/readiness flow.
#[tauri::command]
pub fn get_acp_url() -> Result<String, String> {
    env::var("RIMUSE_ACP_URL")
        .map_err(|_| "RIMUSE_ACP_URL is not set (shell-owned serve lands in P2)".to_string())
}

/// Backend auth secret for the MCP Apps sandbox proxy ("" when unset).
#[tauri::command]
pub fn get_secret_key() -> String {
    env::var("RIMUSE_SERVE_SECRET").unwrap_or_default()
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
