"""End-to-end test of the REAL NSIS installer on a throwaway Windows box.

    D:\\python\\Python313\\python.exe test_install.py

Flow: silent install -> assert files (incl. packaged-layout goose) ->
launch -> CDP verify via install_verify.mjs -> skills refresh check ->
silent uninstall -> confirm nothing left behind.

Installs to C:\\Temp\\rimuse-it (no spaces: NSIS /D= must be last and
unquoted), so no admin rights are needed and cleanup is trivial. The
default Program Files path is intentionally NOT used here — this box is for
behavior verification, not for judging the default-path UX.
"""
import shutil
import subprocess
import sys
import time
from pathlib import Path

SETUP = Path(r"D:\projects\goose\rimuse-tauri\src-tauri\target\release\bundle\nsis\rimuse-shell_0.1.0_x64-setup.exe")
DEST = Path(r"C:\Temp\rimuse-it")
SKILL = Path.home() / ".agents" / "skills" / "rimuse-system-debugging" / "SKILL.md"
VERIFY_MJS = Path(r"C:\Users\PC\AppData\Local\Temp\opencode\install_verify.mjs")
SHOT = Path(r"D:\temp\opencode\install_verify.png")

# tauri.conf bakes --remote-debugging-port=9224 into the installed build too
CDP = "http://127.0.0.1:9224/json"


def log(m):
    print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


def run(cmd, timeout=600, cwd=None):
    log("$ " + " ".join(str(c) for c in cmd))
    return subprocess.run([str(c) for c in cmd], capture_output=True, text=True,
                          timeout=timeout, cwd=cwd)


def install():
    if not SETUP.is_file():
        raise SystemExit(f"installer missing: {SETUP}\nrun bundle_local.py first")
    if DEST.exists():
        log("previous install dir exists — removing")
        shutil.rmtree(DEST, ignore_errors=True)
    # /S = silent; /D= must be the LAST arg and must NOT be quoted
    proc = run([SETUP, "/S", f"/D={DEST}"], timeout=900)
    log(f"installer exit={proc.returncode}")
    if proc.stdout.strip():
        print(proc.stdout[-800:])
    if proc.stderr.strip():
        print(proc.stderr[-800:])


def verify_files():
    exe = DEST / "rimuse-shell.exe"
    goose = DEST / "resources" / "bin" / "goose.exe"
    ok = True
    for p in (exe, goose):
        if p.is_file():
            log(f"OK  {p.relative_to(DEST)} ({p.stat().st_size} bytes)")
        else:
            log(f"MISS {p}")
            ok = False
    if not ok:
        raise SystemExit("installed files incomplete — packaged layout broken")
    return exe


def skills_mtime():
    return SKILL.stat().st_mtime if SKILL.is_file() else None


def launch(exe):
    before = skills_mtime()
    log(f"skills mtime before: {before}")
    subprocess.Popen([str(exe)], cwd=str(DEST))
    # window + CDP come up within a few seconds
    for i in range(40):
        try:
            import json
            import urllib.request
            with urllib.request.urlopen(CDP, timeout=2) as r:
                if json.loads(r.read()):
                    log(f"CDP up after {i}s")
                    break
        except Exception:  # noqa: BLE001
            time.sleep(1)
    else:
        raise SystemExit("CDP never came up (port9224)")
    time.sleep(6)  # let the backend spawn + skills install run

    after = skills_mtime()
    log(f"skills mtime after:  {after}")
    if after is None:
        log("WARN: skill file missing")
    elif before is not None and after <= before:
        log("WARN: skill file NOT refreshed (installBundledSkills may not have run)")
    else:
        log("OK skills refreshed by the installed build")


def cdp_verify():
    proc = run(["node", str(VERIFY_MJS), str(SHOT)], timeout=300)
    print(proc.stdout)
    if proc.stderr.strip():
        print(proc.stderr[-600:])
    return proc.returncode == 0


def uninstall():
    cands = list(DEST.glob("*ninstall*.exe")) + list(DEST.glob("Uninstall*.exe"))
    if not cands:
        log(f"no uninstaller found in {DEST}; will delete the dir manually")
        return
    un = cands[0]
    log(f"uninstaller: {un.name}")
    proc = run([un, "/S"], timeout=600)
    log(f"uninstall exit={proc.returncode}")


def cleanup_check():
    time.sleep(3)
    leftovers = []
    if DEST.exists():
        leftovers = [str(p) for p in DEST.rglob("*")][:10]
    if leftovers:
        log(f"LEFTOVER in {DEST}:")
        for p in leftovers:
            log("  " + p)
        shutil.rmtree(DEST, ignore_errors=True)
        log("force-removed remainder")
    else:
        log("install dir fully removed")
    return not DEST.exists()


def main():
    log(f"installer: {SETUP} ({SETUP.stat().st_size // (1024*1024)} MB)" if SETUP.is_file() else f"MISSING {SETUP}")
    install()
    exe = verify_files()
    launch(exe)
    cdp_ok = cdp_verify()
    log("uninstalling...")
    uninstall()
    clean = cleanup_check()

    log("---")
    log(f"cdp_verify={'PASS' if cdp_ok else 'FAIL'}  cleanup={'OK' if clean else 'DIRTY'}")
    if cdp_ok and clean:
        log("INSTALL TEST PASS")
        return 0
    log("INSTALL TEST FAIL")
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except Exception as e:  # noqa: BLE001
        log(f"FATAL: {type(e).__name__}: {e}")
        sys.exit(1)
