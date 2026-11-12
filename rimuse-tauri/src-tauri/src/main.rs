// Rimuse shell — Tauri2 target (Wails baseline frozen at tag `wails-baseline`).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod shell;

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            shell::get_acp_url,
            shell::get_secret_key,
            shell::get_app_config,
        ])
        .run(tauri::generate_context!())
        .expect("error while running rimuse shell");
}
