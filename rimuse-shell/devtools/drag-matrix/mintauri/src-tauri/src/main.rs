// Minimal Tauri2 app — pure framework baseline for drag/move latency
// comparison against minimal Wails v3.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    tauri::Builder::default()
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
