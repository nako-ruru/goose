"""End-to-end remote build for the Tauri shell (nako-ruru/remote-build flow).

Python replacement for remote_build.ps1 (PS abandoned: parse errors).
Flow: stage -> zip (make_zip.py validation) -> PAT -> sweep stale runs ->
dispatch workflow -> find runner tailnet IP -> upload -> build -> poll ->
download artifact -> shutdown.

Routing policy:
  - api.github.com  -> via proxy http://100.113.111.18:38080 (fallback: direct)
  - tailnet 100.x / localhost -> ALWAYS direct (ProxyHandler({}))
"""
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

REPO = "nako-ruru/remote-build"
WF = "serve-build.yml"
PROXY = "http://100.113.111.18:38080"

STAGE = Path(r"C:\Users\PC\AppData\Local\Temp\opencode\rb-stage")
ZIP_PATH = Path(r"C:\Users\PC\AppData\Local\Temp\opencode\rb-src.zip")
MAKE_ZIP = Path(__file__).resolve().parent / "make_zip.py"
SRC_TAURI = Path(r"D:\projects\goose\rimuse-tauri\src-tauri")
DIST = Path(r"D:\projects\goose\rimuse-shell\frontend\dist")
EXE_OUT = Path(os.environ["TEMP"]) / "rimuse-tauri-shell.exe"
INSTALLER_OUT = Path(os.environ["TEMP"]) / "rimuse-shell-setup.exe"
PYTHON = sys.executable

BUILD_ID = "rimuse" + time.strftime("%m%d-%H%M%S")
SVC_TOKEN = uuid.uuid4().hex
DIR_IN_ZIP = "rimuse-tauri/src-tauri"
# NSIS packaging. The workflow splices build_command into a bash
# double-quoted string (`--build-command "${{ inputs.build_command }}"`),
# so the command must NOT contain quotes, parens, $ or backticks — my first
# attempt had `set "PATH=... (x86) ..."` and bash died with
# `line 11: syntax error near unexpected token '('`. All real logic lives in
# build_nsis.ps1, which rides inside the zip where bash never sees it.
BUILD_CMD = "powershell -NoProfile -ExecutionPolicy Bypass -File build_nsis.ps1"


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


# --- openers: proxy for GitHub, forced-direct for tailnet ---
gh_opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({"https": PROXY, "http": PROXY})
)
direct_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def gh_request(method, path, body=None, timeout=30, retries=3):
    """GitHub API call via proxy (fall back to direct if proxy dies)."""
    url = f"https://api.github.com/{path}"
    data = json.dumps(body).encode() if body is not None else None
    headers = {
        "Authorization": f"Bearer {PAT}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "rimuse-build",
    }
    if data is not None:
        headers["Content-Type"] = "application/json"
    last_err = None
    for i in range(retries):
        for opener in (gh_opener, direct_opener):
            try:
                req = urllib.request.Request(url, data=data, headers=headers, method=method)
                with opener.open(req, timeout=timeout) as resp:
                    raw = resp.read()
                    return json.loads(raw) if raw else None
            except urllib.error.HTTPError as e:
                if e.code == 204:
                    return None
                detail = e.read().decode(errors="replace")[:300]
                last_err = f"HTTP {e.code}: {detail}"
                if e.code in (401, 403, 404):
                    raise RuntimeError(f"{method} {path} -> {last_err}")
                break  # HTTP error != rate/auth: don't retry other opener, just loop
            except Exception as e:  # noqa: BLE001 - network layer, try next opener
                last_err = repr(e)
                continue
        time.sleep(3 * (i + 1))
    raise RuntimeError(f"{method} {path} failed after {retries} tries: {last_err}")


def direct_request(method, url, data=None, headers=None, timeout=30, out_path=None):
    """Tailnet/local call: forced direct, never proxied."""
    req = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    with direct_opener.open(req, timeout=timeout) as resp:
        raw = resp.read()
        if out_path:
            Path(out_path).write_bytes(raw)
            return None
        try:
            return json.loads(raw)
        except Exception:  # noqa: BLE001
            return raw.decode(errors="replace")


def get_pat():
    out = subprocess.run(
        ["git", "credential", "fill"],
        input="protocol=https\nhost=github.com\n\n",
        capture_output=True,
        text=True,
        timeout=15,
    ).stdout
    for line in out.splitlines():
        if line.startswith("password="):
            return line.split("=", 1)[1].strip()
    raise RuntimeError("no github token in git credential fill")


def stage_and_zip():
    log("staging sources...")
    if STAGE.exists():
        shutil.rmtree(STAGE, ignore_errors=True)
    STAGE.mkdir(parents=True)
    for item in SRC_TAURI.iterdir():
        if item.name == "target":
            continue
        if item.is_dir():
            shutil.copytree(item, STAGE / item.name)
        else:
            shutil.copy2(item, STAGE / item.name)
    if (STAGE / "frontend-dist").exists():
        shutil.rmtree(STAGE / "frontend-dist", ignore_errors=True)
    shutil.copytree(DIST, STAGE / "frontend-dist")
    r = subprocess.run([PYTHON, MAKE_ZIP], capture_output=True, text=True, timeout=120)
    print(r.stdout, end="")
    if r.returncode != 0:
        print(r.stderr, file=sys.stderr)
        raise RuntimeError("ZIP VALIDATION FAILED")
    if "ZIP OK" not in r.stdout:
        raise RuntimeError("zip validator did not confirm ZIP OK")


def sweep_stale_runs():
    runs = gh_request("GET", f"repos/{REPO}/actions/workflows/{WF}/runs?per_page=10") or {}
    active = [
        r for r in runs.get("workflow_runs", [])
        if r.get("status") in ("queued", "in_progress", "waiting", "requested")
    ]
    for r in active:
        try:
            gh_request("POST", f"repos/{REPO}/actions/runs/{r['id']}/cancel", body={})
            log(f"cancelled stale run {r['id']}")
        except Exception as e:  # noqa: BLE001
            log(f"cancel {r['id']} failed: {e}")
    if not active:
        log("no stale runs")


def dispatch():
    body = {
        "ref": "main",
        "inputs": {
            "build_id": BUILD_ID,
            "token": SVC_TOKEN,
            "plat": "windows-latest",
            "port": "8787",
            "build_command": BUILD_CMD,
            "timeout": "3600",
            "max_lifetime": "7200",
        },
    }
    gh_request("POST", f"repos/{REPO}/actions/workflows/{WF}/dispatches", body=body)
    log(f"dispatched (204 = ok), build_id={BUILD_ID}")


def wait_for_run():
    time.sleep(6)
    runs = gh_request("GET", f"repos/{REPO}/actions/workflows/{WF}/runs?per_page=1") or {}
    r = (runs.get("workflow_runs") or [{}])[0]
    log(f"run id = {r.get('id')} (status={r.get('status')})")
    return r.get("id")


def parse_peers():
    out = subprocess.run(["tailscale", "status"], capture_output=True, text=True, timeout=20).stdout
    peers = {}
    for line in out.splitlines():
        m = re.match(r"\s*(\d+\.\d+\.\d+\.\d+)\s+(\S+)", line)
        if m:
            peers[m.group(1)] = m.group(2)
    return peers


def find_runner(baseline, run_id=None):
    deadline = time.time() + 12 * 60
    while time.time() < deadline:
        # Watch the workflow itself: a run that dies (bash syntax error, bad
        # input, ...) never spawns a runner, so waiting the full 12 min would
        # only hide the real error behind a generic timeout.
        if run_id:
            try:
                run = gh_request("GET", f"repos/{REPO}/actions/runs/{run_id}") or {}
                if run.get("status") == "completed" and run.get("conclusion") != "success":
                    raise RuntimeError(
                        f"workflow {run_id} concluded '{run.get('conclusion')}' before a "
                        f"runner joined — see https://github.com/{REPO}/actions/runs/{run_id}"
                    )
            except RuntimeError:
                raise
            except Exception:  # noqa: BLE001 - transient API hiccup: keep waiting
                pass
        for ip, name in parse_peers().items():
            if "github-runner" not in name or ip in baseline:
                continue
            try:
                body = direct_request(
                    "GET", f"http://{ip}:8787/healthz?token={SVC_TOKEN}", timeout=5
                )
                if "ok" in str(body):
                    log(f"runner IP verified: {ip}")
                    return ip
            except Exception:  # noqa: BLE001
                continue
        time.sleep(10)
    raise RuntimeError("runner never joined tailnet (12min)")


def build_flow(base):
    # upload
    log("uploading zip...")
    direct_request(
        "PUT",
        f"{base}/files/src.zip?token={SVC_TOKEN}",
        data=ZIP_PATH.read_bytes(),
        headers={"Content-Type": "application/octet-stream"},
        timeout=300,
    )
    log("upload done")

    # trigger
    body = {"zip": "src.zip", "dir": DIR_IN_ZIP, "command": BUILD_CMD}
    direct_request(
        "POST",
        f"{base}/build?token={SVC_TOKEN}",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
        timeout=30,
    )
    log("build triggered")

    # poll
    deadline = time.time() + 40 * 60
    phase = "building"
    st = {}
    while time.time() < deadline:
        time.sleep(10)
        try:
            st = direct_request("GET", f"{base}/status?token={SVC_TOKEN}", timeout=10) or {}
            phase = st.get("phase", "?")
            if phase in ("done", "error"):
                break
        except Exception:  # noqa: BLE001
            log("status poll error (retrying)")
    log(f"final phase: {phase}")
    if st.get("message"):
        log(f"message: {st['message']}")
    if phase != "done":
        tail = (st.get("log") or "").splitlines()[-30:]
        print("\n".join(tail))
        raise RuntimeError(f"BUILD FAILED: {phase}")

    # download: the bare exe (feeds the portable zip) AND the NSIS installer
    for rel, out in (
        (f"{DIR_IN_ZIP}/target/release/rimuse-shell.exe", EXE_OUT),
        (f"{DIR_IN_ZIP}/target/release/rimuse-installer.exe", INSTALLER_OUT),
    ):
        log(f"downloading {out.name}...")
        direct_request("GET", f"{base}/files/{rel}?token={SVC_TOKEN}", timeout=600, out_path=out)
        log(f"ARTIFACT OK: {out} ({out.stat().st_size // (1024*1024)} MB)")

    # sync the runner's Cargo.lock back (cargo resolves new deps remotely;
    # only accept it when it actually carries the new dependency)
    lock_tmp = Path(os.environ["TEMP"]) / "rimuse-Cargo.lock"
    try:
        direct_request(
            "GET",
            f"{base}/files/{DIR_IN_ZIP}/Cargo.lock?token={SVC_TOKEN}",
            timeout=30,
            out_path=lock_tmp,
        )
        text = lock_tmp.read_text(encoding="utf-8", errors="replace")
        if "include_dir" in text:
            shutil.copy2(lock_tmp, Path(r"D:\projects\goose\rimuse-tauri\src-tauri\Cargo.lock"))
            log("Cargo.lock synced back from runner")
        else:
            log("Cargo.lock sync skipped (no new deps in runner lock)")
    except Exception as e:  # noqa: BLE001
        log(f"Cargo.lock sync skipped: {e}")


def main():
    global PAT
    log(f"svcToken={SVC_TOKEN} (archive: reusable for retry)")
    stage_and_zip()

    PAT = get_pat()
    log(f"PAT ok ({len(PAT)} chars)")

    sweep_stale_runs()
    dispatch()
    run_id = wait_for_run()

    baseline = set(parse_peers())
    log(f"baseline tailnet peers: {len(baseline)}")
    runner_ip = find_runner(baseline, run_id)

    base = f"http://{runner_ip}:8787"
    build_flow(base)

    try:
        direct_request("GET", f"{base}/shutdown?token={SVC_TOKEN}", timeout=5)
        log("service shutdown")
    except Exception:  # noqa: BLE001
        pass
    log("ALL DONE")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001
        log(f"FATAL: {e}")
        sys.exit(1)
