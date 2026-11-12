"""One-shot local NSIS bundling (replaces the5 manual steps).

    D:\\python\\Python313\\python.exe bundle_local.py

Stages the compiled exe + frontend dist + goose backend, runs
`cargo-tauri bundle` (which packages WITHOUT compiling — compilation stays
on the remote runner), then verifies the installer actually contains
goose.exe. Everything local: zero CI, zero upload.

Why local: goose.exe is164 MB and already lives here; shipping it to the
runner just to stuff it into an installer was backwards. Only Rust changes
still need the runner.
"""
import shutil
import subprocess
import sys
import time
from pathlib import Path

TOOLS = Path.home() / "AppData" / "Local" / "Rimuse" / "build-tools"
TAURI_CLI = TOOLS / "tauri" / "cargo-tauri.exe"
CLI_ZIP = Path.home() / "AppData" / "Local" / "Temp" / "tauri-cli-probe.zip"

SRC_TAURI = Path(r"D:\projects\goose\rimuse-tauri\src-tauri")
SHELL_EXE = Path.home() / "AppData" / "Local" / "Temp" / "rimuse-tauri-shell.exe"
FRONTEND_DIST = Path(r"D:\projects\goose\rimuse-shell\frontend\dist")
GOOSE_EXE = Path(r"D:\goose\resources\bin\goose.exe")

BUNDLE_OUT = SRC_TAURI / "target" / "release" / "bundle" / "nsis"
SETUP_NAME = "rimuse-shell_0.1.0_x64-setup.exe"


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def ensure_tauri_cli():
    if TAURI_CLI.is_file():
        log(f"tauri-cli: {TAURI_CLI}")
        return
    if not CLI_ZIP.is_file():
        raise SystemExit(f"missing {CLI_ZIP} — download the prebuilt tauri-cli first")
    TOOLS.joinpath("tauri").mkdir(parents=True, exist_ok=True)
    shutil.unpack_archive(str(CLI_ZIP), str(TOOLS / "tauri"), format="zip")
    if not TAURI_CLI.is_file():
        raise SystemExit("cargo-tauri.exe missing after extract")
    log(f"tauri-cli extracted: {TAURI_CLI}")


def stage():
    """Compiled exe + dist + backend must sit where tauri.conf expects them."""
    for src, dst in (
        (SHELL_EXE, SRC_TAURI / "target" / "release" / "rimuse-shell.exe"),
        (FRONTEND_DIST, SRC_TAURI / "frontend-dist"),
        (GOOSE_EXE, SRC_TAURI / "resources" / "bin" / "goose.exe"),
    ):
        if not src.exists():
            raise SystemExit(f"missing input: {src}")
        if dst.is_dir():
            shutil.rmtree(dst)
        dst.parent.mkdir(parents=True, exist_ok=True)
        if src.is_dir():
            shutil.copytree(src, dst)
        else:
            shutil.copy2(src, dst)
        size = sum(f.stat().st_size for f in dst.rglob("*") if f.is_file()) if dst.is_dir() else dst.stat().st_size
        log(f"staged {dst.relative_to(SRC_TAURI)} ({size // (1024*1024)} MB)")


def bundle():
    # tauri downloads its own NSIS + nsis_tauri_utils.dll (hash-checked),
    # so no system NSIS is needed. --ci skips interactive prompts.
    #
    # bundle.resources is injected HERE rather than living in tauri.conf.json:
    # tauri-build's build.rs validates resource paths at COMPILE time, so a
    # committed resources entry forces every remote compile to carry the164 MB
    # goose.exe (that was the56 MB source zip). The runner only compiles, so
    # it does not need the entry; packaging happens locally, where stage() has
    # already placed the real binary.
    cmd = [
        str(TAURI_CLI), "bundle", "--bundles", "nsis", "--ci",
        "--config",
        '{"bundle":{"resources":["resources/bin/goose.exe"]}}',
    ]
    log("running: " + " ".join(cmd))
    proc = subprocess.run(cmd, cwd=SRC_TAURI, capture_output=True, text=True, timeout=1800)
    tail = (proc.stdout + proc.stderr).strip().splitlines()[-15:]
    for line in tail:
        print("  " + line)
    if proc.returncode != 0:
        raise SystemExit(f"tauri bundle failed (exit {proc.returncode})")
    setup = BUNDLE_OUT / SETUP_NAME
    if not setup.is_file():
        raise SystemExit(f"expected output missing: {setup}")
    return setup


def verify(setup):
    """List the installer and assert both binaries are inside."""
    log(f"installer: {setup} ({setup.stat().st_size // (1024*1024)} MB)")
    vi = subprocess.run(
        ["powershell", "-NoProfile", "-Command",
         f"(Get-Item '{setup}').VersionInfo.ProductName"],
        capture_output=True, text=True, timeout=60,
    ).stdout.strip()
    log(f"ProductName: {vi or '(none)'}")

    sevenzip = shutil.which("7z") or shutil.which("7za")
    if not sevenzip:
        log("7z not available — skipping content listing")
        return
    listing = subprocess.run([sevenzip, "l", str(setup)],
                             capture_output=True, text=True, timeout=300).stdout
    need = ("rimuse-shell.exe", "resources\\bin\\goose.exe", "resources/bin/goose.exe")
    hits = [n for n in need if n in listing]
    for line in listing.splitlines():
        if "rimuse-shell.exe" in line or "goose.exe" in line:
            log("  " + line.strip())
    if "rimuse-shell.exe" not in listing:
        raise SystemExit("rimuse-shell.exe NOT in installer")
    if not ("resources\\bin\\goose.exe" in listing or "resources/bin/goose.exe" in listing):
        raise SystemExit("goose.exe NOT in installer — packaged layout would be broken")
    log(f"content OK ({len(hits)} required entries found)")


def main():
    ensure_tauri_cli()
    stage()
    setup = bundle()
    verify(setup)
    log(f"DONE: {setup}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except Exception as e:  # noqa: BLE001
        log(f"FATAL: {type(e).__name__}: {e}")
        sys.exit(1)
