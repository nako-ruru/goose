// Shell-owned goose serve lifecycle — Rust port of rimuse-shell/serve.go
// (Wails baseline, tag `wails-baseline`), P2 of the Tauri migration.
//
// Behavior parity with the Go version (all verified there):
//  1. binary discovery: GOOSE_BINARY (dev) -> resources/bin next to the exe
//     -> flat layout next to the exe -> PATH lookup; all-miss = clear error
//  2. port: OS-assigned free port on 127.0.0.1 (never fixed dev ports)
//  3. env: inherit + GOOSE_SERVER__SECRET_KEY injected (per-run random)
//  4. readiness: GET /status every100ms up to30s, fail-fast if the child
//     dies, timeout errors carry the log tail
//  5. stdout/stderr kernel-direct to the log file (no in-process copy —
//     the Go version measured ~30% core burned by an in-process pipe copy)
//  6. child.pid + start-time recorded; next start reaps an orphan only on
//     exact pid+start match (PID-reuse safe) via taskkill /t
//  7. graceful stop = taskkill tree by PID, idempotent (RunEvent::Exit)
//  8. crash policy: no in-place loop — state marked dead, next GetAcpUrl
//     respawns on demand (renderer reconnects and asks again)
//
// Concurrency design (deadlock-free by construction):
//  - the reaper thread OWNS the Child and only ever blocks in child.wait();
//    nobody else touches the Child, so stop()/timeout can never block
//    behind a lock the reaper holds for the child's whole lifetime
//  - all kills go through kill_tree(pid)
//  - BACKEND lock is only held for short state reads/writes; the readiness
//    loop reads the CHILD_EXITED atomic WITHOUT the lock... wait, no: the
//    loop runs under the BACKEND lock (serializing concurrent spawns), but
//    it observes child death via the atomic, which the reaper stores
//    BEFORE attempting the BACKEND lock — so no circular wait.
//
// Dev override: RIMUSE_ACP_URL keeps pointing at an external serve and
// skips spawning entirely (same contract as the Wails shell).

use std::fs;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const READINESS_TIMEOUT: Duration = Duration::from_secs(30);
const READINESS_INTERVAL: Duration = Duration::from_millis(100);
const TAIL_BYTES: usize = 16 * 1024;
#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

struct Backend {
    pid: u32, // owned child, 0 = none
    port: u16,
    secret: String,
    running: bool,
}

static BACKEND: Mutex<Backend> = Mutex::new(Backend {
    pid: 0,
    port: 0,
    secret: String::new(),
    running: false,
});
/// Set by the reaper the moment its child exits (BEFORE it takes the
/// BACKEND lock); read by the readiness loop and the fast path.
static CHILD_EXITED: AtomicBool = AtomicBool::new(false);

fn cache_dir() -> PathBuf {
    let base = std::env::var("LOCALAPPDATA")
        .map(PathBuf::from)
        .or_else(|_| std::env::var("TEMP").map(PathBuf::from))
        .unwrap_or_else(|_| PathBuf::from("."));
    base.join("rimuse")
}

fn serve_log_path() -> PathBuf {
    let dir = cache_dir().join("logs");
    let _ = fs::create_dir_all(&dir);
    dir.join("goose-serve.log")
}

fn child_pid_file() -> PathBuf {
    cache_dir().join("child.pid")
}

fn gen_secret() -> String {
    let mut buf = [0u8; 32];
    getrandom::fill(&mut buf).expect("crypto randomness unavailable");
    buf.iter().map(|b| format!("{b:02x}")).collect()
}

fn find_goose_binary() -> Result<PathBuf, String> {
    if let Ok(env_bin) = std::env::var("GOOSE_BINARY") {
        let p = PathBuf::from(&env_bin);
        if p.is_file() {
            return Ok(p);
        }
        return Err(format!("invalid GOOSE_BINARY path: {env_bin}"));
    }

    let exe = std::env::current_exe().map_err(|e| format!("exe path: {e}"))?;
    let dir = exe.parent().unwrap_or(Path::new("."));
    let name = if cfg!(windows) { "goose.exe" } else { "goose" };

    let mut candidates = vec![
        dir.join("resources").join("bin").join(name), // packaged layout
        dir.join(name),                               // flat layout
    ];
    if let Ok(path_env) = std::env::var("PATH") {
        for p in std::env::split_paths(&path_env) {
            candidates.push(p.join(name));
        }
    }
    for c in &candidates {
        if c.is_file() {
            return Ok(c.clone());
        }
    }
    Err(format!(
        "goose binary not found (tried: {}); set GOOSE_BINARY in development",
        candidates
            .iter()
            .map(|c| c.display().to_string())
            .collect::<Vec<_>>()
            .join(", ")
    ))
}

fn find_free_port() -> Result<u16, String> {
    let listener = TcpListener::bind(("127.0.0.1", 0)).map_err(|e| format!("bind: {e}"))?;
    let port = listener.local_addr().map_err(|e| e.to_string())?.port();
    drop(listener);
    Ok(port)
}

fn read_log_tail(path: &Path) -> String {
    let Ok(data) = fs::read(path) else {
        return String::new();
    };
    let start = data.len().saturating_sub(TAIL_BYTES);
    let text = String::from_utf8_lossy(&data[start..]);
    let lines: Vec<&str> = text.lines().collect();
    let take = lines.len().saturating_sub(40);
    lines[take..].join("\n")
}

fn status_ok(port: u16) -> bool {
    let Ok(mut stream) = TcpStream::connect(("127.0.0.1", port)) else {
        return false;
    };
    let _ = stream.set_read_timeout(Some(Duration::from_millis(800)));
    let _ = stream.set_write_timeout(Some(Duration::from_millis(800)));
    if stream
        .write_all(b"GET /status HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n")
        .is_err()
    {
        return false;
    }
    let mut buf = [0u8; 128];
    match stream.read(&mut buf) {
        Ok(n) => {
            let head = String::from_utf8_lossy(&buf[..n.min(128)]);
            head.starts_with("HTTP/1.0 200") || head.starts_with("HTTP/1.1 200")
        }
        Err(_) => false,
    }
}

fn writeln_log(path: &Path, line: &str) {
    if let Ok(mut f) = fs::OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(f, "{line}");
    }
}

/// Orphan reaping: read the recorded child of a previous run; kill it only
/// when BOTH pid and process creation time match (PID-reuse safe).
fn sweep_orphans() {
    let Ok(content) = fs::read_to_string(child_pid_file()) else {
        return;
    };
    let mut parts = content.split_whitespace();
    let (Some(pid_s), Some(start_s)) = (parts.next(), parts.next()) else {
        return;
    };
    let (Ok(pid), Ok(start)) = (pid_s.parse::<u32>(), start_s.parse::<i64>()) else {
        return;
    };
    if process_start_unix(pid) == Some(start) {
        eprintln!("[serve] reaping orphan backend from previous run: pid={pid}");
        kill_tree(pid);
    }
}

/// Windows: creation time via OpenProcess + GetProcessTimes (FILETIME -> unix s).
fn process_start_unix(pid: u32) -> Option<i64> {
    #[cfg(windows)]
    {
        use windows_sys::Win32::Foundation::{CloseHandle, FILETIME};
        use windows_sys::Win32::System::Threading::{
            GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION,
        };
        unsafe {
            let handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
            if handle.is_null() {
                return None;
            }
            let mut creation = FILETIME {
                dwLowDateTime: 0,
                dwHighDateTime: 0,
            };
            let mut exit = FILETIME {
                dwLowDateTime: 0,
                dwHighDateTime: 0,
            };
            let mut kernel = FILETIME {
                dwLowDateTime: 0,
                dwHighDateTime: 0,
            };
            let mut user = FILETIME {
                dwLowDateTime: 0,
                dwHighDateTime: 0,
            };
            let ok = GetProcessTimes(handle, &mut creation, &mut exit, &mut kernel, &mut user);
            CloseHandle(handle);
            if ok == 0 {
                return None;
            }
            const EPOCH_DIFF: i64 = 11_644_473_600_000_000;
            let ticks =
                ((creation.dwHighDateTime as i64) << 32) | creation.dwLowDateTime as i64;
            Some((ticks - EPOCH_DIFF) / 10_000_000)
        }
    }
    #[cfg(not(windows))]
    {
        let _ = pid;
        None
    }
}

fn kill_tree(pid: u32) {
    #[cfg(windows)]
    let _ = Command::new("taskkill")
        .args(["/pid", &pid.to_string(), "/f", "/t"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status();
    #[cfg(not(windows))]
    let _ = Command::new("kill")
        .args(["-TERM", &pid.to_string()])
        .status();
}

fn record_child(pid: u32, start_unix: i64) {
    let _ = fs::create_dir_all(cache_dir());
    let _ = fs::write(child_pid_file(), format!("{pid} {start_unix}"));
}

/// Ensure a healthy owned backend; returns (acp_url, secret).
pub fn ensure_running() -> Result<(String, String), String> {
    let mut be = BACKEND.lock().unwrap_or_else(|e| e.into_inner());

    if be.running && !CHILD_EXITED.load(Ordering::SeqCst) {
        return Ok((acp_url(be.port, &be.secret), be.secret.clone()));
    }

    // Reap the previous instance's backend before (re)starting ours —
    // a force-killed shell never reaches the exit hook.
    sweep_orphans();

    if be.secret.is_empty() {
        be.secret = gen_secret();
    }

    let binary = find_goose_binary()?;
    let port = find_free_port()?;

    let log_path = serve_log_path();
    let log_file = fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)
        .map_err(|e| format!("cannot open serve log: {e}"))?;
    let log_err = log_file
        .try_clone()
        .map_err(|e| format!("serve log clone: {e}"))?;
    writeln_log(
        &log_path,
        &format!(
            "=== [{}] starting {} --port {port}",
            chrono_free_now(),
            binary.display()
        ),
    );

    let mut cmd = Command::new(&binary);
    cmd.args([
        "serve",
        "--platform",
        "desktop",
        "--enable-scheduler",
        "--host",
        "127.0.0.1",
        "--port",
        &port.to_string(),
        "--allowed-origin",
        "http://wails.localhost",
        "--allowed-origin",
        "http://tauri.localhost",
    ])
    .env("GOOSE_SERVER__SECRET_KEY", &be.secret)
    .stdin(Stdio::null())
    .stdout(Stdio::from(log_file))
    .stderr(Stdio::from(log_err));
    if let Ok(cwd) = std::env::current_dir() {
        cmd.current_dir(cwd);
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }

    let child = cmd
        .spawn()
        .map_err(|e| format!("failed to spawn goose serve: {e}"))?;
    let pid = child.id();
    record_child(pid, process_start_unix(pid).unwrap_or(0));

    // Reset BEFORE spawn takes effect... i.e. before the new reaper can
    // possibly store true: the OLD reaper already stored true (that is why
    // we are respawning), and it will not store again.
    CHILD_EXITED.store(false, Ordering::SeqCst);
    be.pid = pid;

    // Reaper OWNS the child: it blocks only in child.wait() and never
    // shares the Child with anyone, so stop()/timeout cannot deadlock.
    // It flips the atomic FIRST (readers need it before we take the lock),
    // then clears `running` only if its pid is still the current one (an
    // old reaper must not clobber a newer child's state).
    {
        let log_path = log_path.clone();
        std::thread::spawn(move || {
            let mut child: Child = child;
            let status = child.wait();
            CHILD_EXITED.store(true, Ordering::SeqCst);
            let code = status.map(|s| s.code().unwrap_or(-1)).unwrap_or(-1);
            eprintln!("[serve] goose serve exited (code={code})");
            writeln_log(&log_path, &format!("=== exited code={code}"));
            let mut be = BACKEND.lock().unwrap_or_else(|e| e.into_inner());
            if be.pid == pid {
                be.running = false;
            }
        });
    }

    // Readiness: poll /status; observe child death via the atomic (stored
    // by the reaper before it contends for our lock — no circular wait).
    let deadline = Instant::now() + READINESS_TIMEOUT;
    loop {
        if CHILD_EXITED.load(Ordering::SeqCst) {
            return Err(format!(
                "goose serve exited during startup (see {})\n{}",
                log_path.display(),
                read_log_tail(&log_path)
            ));
        }
        if status_ok(port) {
            eprintln!("[serve] ready on 127.0.0.1:{port}");
            writeln_log(
                &log_path,
                &format!("=== [{}] ready on 127.0.0.1:{port}", chrono_free_now()),
            );
            be.port = port;
            be.running = true;
            return Ok((acp_url(port, &be.secret), be.secret.clone()));
        }
        if Instant::now() >= deadline {
            kill_tree(pid); // pid-based: never contends with the reaper
            return Err(format!(
                "goose serve not ready after 30s (see {})\n{}",
                log_path.display(),
                read_log_tail(&log_path)
            ));
        }
        std::thread::sleep(READINESS_INTERVAL);
    }
}

fn chrono_free_now() -> String {
    // RFC3339-ish timestamp without pulling a datetime crate.
    let d = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    format!("unix:{d}")
}

pub fn acp_url(port: u16, secret: &str) -> String {
    format!("ws://127.0.0.1:{port}/acp?token={secret}")
}

/// Managed secret (stable across respawns within one shell run).
pub fn managed_secret() -> String {
    let mut be = BACKEND.lock().unwrap_or_else(|e| e.into_inner());
    if be.secret.is_empty() {
        be.secret = gen_secret();
    }
    be.secret.clone()
}

/// Graceful stop (Tauri RunEvent::Exit): kill the tree by pid, idempotent.
/// Never touches the Child handle — the reaper owns it — so this can not
/// block behind child.wait().
pub fn stop() {
    let mut be = BACKEND.lock().unwrap_or_else(|e| e.into_inner());
    if be.pid != 0 && !CHILD_EXITED.load(Ordering::SeqCst) {
        kill_tree(be.pid);
    }
    be.pid = 0;
    be.running = false;
    eprintln!("[serve] backend stopped");
}
