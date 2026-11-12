// Rimuse shell — Tauri2 target (Wails baseline frozen at tag `wails-baseline`).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod serve;
mod shell;
mod skills;

fn main() {
    // Bundled skills install — parity with Wails main.go installBundledSkills,
    // which runs before the app object is created.
    skills::install_bundled_skills();

    let app = tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            shell::get_acp_url,
            shell::get_secret_key,
            shell::get_app_config,
            shell::open_standalone,
            shell::close_window,
            shell::window_id,
        ])
        .build(tauri::generate_context!())
        .expect("error while running rimuse shell");

    app.run(|_handle, event| {
        if let tauri::RunEvent::Exit = event {
            // Reap the shell-owned goose serve child on exit (taskkill tree)
            // so no orphaned backend survives the app — serve.rs stop().
            serve::stop();
        }
    });
}
