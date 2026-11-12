"""Assemble the免安装 (portable) distribution zip.

Layout matches what serve.rs::find_goose_binary expects as the "packaged
layout": the shell exe at the root, goose under resources/bin/ next to it.
Forward slashes throughout (SERVE_BUILD_AI.md trap: Compress-Archive stores
backslashes and downstream extractors then lose the directory tree).
"""
import sys
import zipfile
from pathlib import Path

VERSION = "0.1.0"
OUT = Path.home() / "AppData" / "Local" / "Temp" / f"rimuse-shell-{VERSION}-windows-x64.zip"
SHELL_EXE = Path.home() / "AppData" / "Local" / "Temp" / "rimuse-tauri-shell.exe"
GOOSE_EXE = Path(r"D:\goose\resources\bin\goose.exe")

# (source path, entry name inside the zip)
ENTRIES = [
    (SHELL_EXE, "rimuse-shell/rimuse-tauri-shell.exe"),
    (GOOSE_EXE, "rimuse-shell/resources/bin/goose.exe"),
]


def main() -> int:
    missing = [str(src) for src, _ in ENTRIES if not src.is_file()]
    if missing:
        print("MISSING:", *missing, sep="\n  ")
        return 1

    if OUT.exists():
        OUT.unlink()

    with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
        for src, arcname in ENTRIES:
            # compresslevel=6 keeps the 164 MB goose from ballooning further;
            # exes are already internally compressed, so gains are modest.
            z.write(src, arcname)

    # validate: forward slashes, expected members, no backslashes anywhere
    with zipfile.ZipFile(OUT) as z:
        names = z.namelist()
        bad = [n for n in names if "\\" in n]
        expect = {arc for _, arc in ENTRIES}
        if bad:
            print("BACKSLASH ENTRIES:", bad)
            return 1
        if not expect.issubset(set(names)):
            print("MISSING ENTRIES:", expect - set(names))
            return 1
        if z.testzip() is not None:
            print("ZIP CORRUPT")
            return 1

    size_mb = OUT.stat().st_size / (1024 * 1024)
    print(f"entries={len(names)} size={size_mb:.1f}MB backslash=0 OK")
    for n in names:
        print(f"  {n}")
    print(f"ARTIFACT: {OUT}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
