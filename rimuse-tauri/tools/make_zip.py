"""Zip the staged sources with FORWARD slashes (SERVE_BUILD_AI.md trap #6:
Compress-Archive stores backslashes; remote extraction then has no dirs).
Also validates: every entry uses '/', Cargo.toml present."""
import os
import sys
import zipfile

STAGE = r"C:\Users\PC\AppData\Local\Temp\opencode\rb-stage"
OUT = r"C:\Users\PC\AppData\Local\Temp\opencode\rb-src.zip"
EXCLUDE_DIRS = {"target", "node_modules", ".git"}

def main():
    if os.path.exists(OUT):
        os.remove(OUT)
    count = 0
    with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as z:
        for root, dirs, files in os.walk(STAGE):
            dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS]
            for f in files:
                full = os.path.join(root, f)
                rel = os.path.relpath(full, STAGE).replace(os.sep, "/")
                z.write(full, rel)
                count += 1
    # validate
    bad = []
    cargo_ok = False
    dist_ok = False
    with zipfile.ZipFile(OUT) as z:
        for n in z.namelist():
            if "\\" in n:
                bad.append(n)
            if n == "Cargo.toml":
                cargo_ok = True
            if n == "frontend-dist/index.html":
                dist_ok = True
    size = os.path.getsize(OUT)
    print(f"entries={count} size={size//1024}KB backslash_entries={len(bad)} cargo_toml={cargo_ok} dist={dist_ok}")
    if bad:
        print("BAD:", bad[:5])
        sys.exit(1)
    if not cargo_ok:
        print("MISSING rimuse-tauri/src-tauri/Cargo.toml")
        sys.exit(1)
    print("ZIP OK")

if __name__ == "__main__":
    main()
