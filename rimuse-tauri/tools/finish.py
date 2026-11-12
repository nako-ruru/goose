"""Finish a build whose script died before download/shutdown (usually
because the running shell held a lock on the exe).

    python finish.py <base-url> <token> [artifact-rel-path...]

Defaults to the bare exe. Kills the shell first to release the file lock.
"""
import json
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

TEMP = Path.home() / "AppData" / "Local" / "Temp"
EXE_OUT = TEMP / "rimuse-tauri-shell.exe"
ROOT = "rimuse-tauri/src-tauri/target/release"

DIRECT = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def log(m):
    print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


def kill_shell():
    subprocess.run(["taskkill", "/IM", "rimuse-tauri-shell.exe", "/F"],
                   capture_output=True, text=True)
    time.sleep(1.5)
    out = subprocess.run(["tasklist", "/FI", "IMAGENAME eq rimuse-tauri-shell.exe"],
                         capture_output=True, text=True).stdout
    return "rimuse-tauri-shell.exe" not in out


def get(url, out_path, timeout=600):
    req = urllib.request.Request(url, method="GET")
    with DIRECT.open(req, timeout=timeout) as r:
        out_path.write_bytes(r.read())


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    base = sys.argv[1].rstrip("/")
    token = sys.argv[2]
    rels = sys.argv[3:] or [f"{ROOT}/rimuse-shell.exe"]

    log(f"releasing file lock: {kill_shell()}")

    for rel in rels:
        out = EXE_OUT if rel.endswith("rimuse-shell.exe") else TEMP / Path(rel).name
        url = f"{base}/files/{rel}?token={token}"
        for attempt in range(3):
            try:
                log(f"downloading {out.name} (attempt {attempt + 1})...")
                get(url, out)
                head = out.read_bytes()[:2]
                log(f"OK {out} ({out.stat().st_size} bytes, {'PE' if head == b'MZ' else 'NOT-PE!'})")
                break
            except Exception as e:  # noqa: BLE001
                log(f"  failed: {type(e).__name__}: {e}")
                if attempt == 2:
                    raise
                kill_shell()
                time.sleep(3)

    try:
        get(f"{base}/shutdown?token={token}", TEMP / "shutdown-ack.txt", timeout=8)
        log("service shutdown")
    except Exception as e:  # noqa: BLE001
        log(f"shutdown skipped: {e}")
    log("ALL DONE")
    return 0


if __name__ == "__main__":
    sys.exit(main())
