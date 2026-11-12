# rimuse-tauri

Tauri2 shell for Rimuse. The Wails baseline is frozen at tag `wails-baseline`
(`rimuse-shell/`), so both shells build from the same frontend tree and stay
behavior-comparable.

## Build pipeline

| Phase | Where | Why |
|---|---|---|
| **Compile** (`cargo build`) | remote runner (`nako-ruru/remote-build`) | the dev machine is slow; only Rust changes need a CI run |
| **Package** (`cargo-tauri bundle`) | **local** | `bundle` does NOT compile, and the164 MB `goose.exe` already lives here — uploading it to the runner just to stuff it into an installer was backwards |

```
local: bundle_local.py        -> target/release/bundle/nsis/*-setup.exe
local: test_install.py        -> silent install, CDP verify, uninstall
CI   : remote_build.py        -> only when Rust sources change
```

Each CI run uploads ~3 MB of source (no incremental) and downloads the exe.
Packaging costs zero CI.

## Traps already hit (all found by reading source, not docs)

###1. Sync command that creates a window deadlocks the whole app

**Symptom:** the invoke never settles, and from then on *every* invoke hangs
(`get_app_config` too) — the app looks alive (`Responding=True`) but IPC is
dead.

**Cause:** `tauri-runtime-wry`'s `create_window` says it outright:

> Note that this must be called from a separate thread, otherwise the channel
> will introduce a deadlock.

It dispatches to the event loop and blocks for the result. A *sync* command
runs inline on that same event loop thread → it waits for itself.

**Fix:** window commands must be `async fn` (execution moves to a tokio
worker). See `open_standalone` / `close_window` in `src/shell.rs`.

###2. Second window fails with `0x8007139F` (ERROR_INVALID_STATE)

**Symptom:** `open_standalone` returns
`WebView2 error: WindowsError(... HRESULT(0x8007139F))`, no window appears.

**Cause:** WebView2 requires **every window sharing one user data folder to be
created with identical `AdditionalBrowserArguments`** (tauri#8196). The main
window sets `--remote-debugging-port=9224` in `tauri.conf`; a runtime-created
window without it falls through to wry's default `--disable-features=...` →
mismatch → rejected. The browser process already exists and cannot re-apply
switches.

**Fix:** runtime windows repeat the configured args via the `BROWSER_ARGS`
const, which is pinned to the tauri.conf value. The browser process is reused,
so the debug port is not bound twice.

###3. `build_command` is spliced into a bash string by the workflow

**Symptom:** the `Serve` step dies in2 minutes with
`line11: syntax error near unexpected token '('` — and nothing else looks
wrong.

**Cause:** `serve-build.yml` does:

```yaml
shell: bash
run: |
  ...
  --build-command "${{ inputs.build_command }}" \
```

So the command is parsed **twice**: once by bash (double-quoted), once by the
service (`shell=True` → cmd.exe). My first command contained
`set "PATH=%PATH%;C:\Program Files (x86)\NSIS"` — the `"` closed the bash
string early and `(x86)` became bare bash syntax.

**Fix:** `build_command` must contain **no quotes, parens, `$` or backticks**.
All real logic lives in `build_nsis.ps1`, which travels inside the zip where
bash never sees it. The command is now one zero-metacharacter line.

Also: `remote_build.py` polls the workflow run while waiting for a runner, so
a dead run fails in seconds with a run URL instead of hanging12 min on a
generic "runner never joined".

## Layout

```
rimuse-tauri/
├── README.md          this file — build pipeline + the traps below
├── .gitignore         ours (repo root one must stay upstream-identical)
├── tools/             build & test scripts, run from here
│   ├── remote_build.py    CI orchestrator (compile remotely)
│   ├── make_zip.py        forward-slash zip + validation
│   ├── bundle_local.py    local NSIS packaging (no CI)
│   ├── make_dist.py       portable zip assembly
│   ├── test_install.py    silent install -> verify -> uninstall
│   └── install_verify.mjs CDP checks used by test_install.py
└── src-tauri/
    ├── src/serve.rs      shell-owned backend lifecycle (spawn/readiness/orphan/exit/respawn)
    ├── src/shell.rs      ShellService commands incl. the window trio
    ├── src/skills.rs     bundled skills -> ~/.agents/skills on every launch
    ├── build_nsis.ps1    packaging logic (rides in the zip; bash never parses it)
    ├── skills/           embedded skill sources (tracked)
    ├── resources/        staged goose.exe for local bundling (gitignored,164 MB)
    ├── frontend-dist/    staged vite output (gitignored)
    └── target/           build output (gitignored)
```

`src-tauri/.gitignore` is deliberately separate from the repo root: the
upstream goose `.gitignore` must stay byte-identical.

The scripts hardcode this machine's paths (`D:\projects\goose\...`, the LAN
proxy) — they are dev-machine tooling, not portable CI.
