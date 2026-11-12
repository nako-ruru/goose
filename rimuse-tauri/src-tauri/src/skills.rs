// Bundled skills installer — port of rimuse-shell/main.go installBundledSkills
// (Wails baseline, tag `wails-baseline`).
//
// Skills ship embedded in the binary (include_dir, the Tauri counterpart of
// go:embed all:skills) and are re-copied to ~/.agents/skills on every launch,
// overwriting in place, so skill updates ride along with app updates.
// Failures are logged and never fatal — the app works without them.

use include_dir::{include_dir, Dir, DirEntry};
use std::env;
use std::ffi::OsString;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};

static SKILLS: Dir = include_dir!("$CARGO_MANIFEST_DIR/skills");

/// Copy the embedded skills tree to ~/.agents/skills (overwrite in place).
pub fn install_bundled_skills() {
    let Some(home) = home_dir() else {
        eprintln!("[skills] cannot resolve home dir");
        return;
    };
    let dest_root = home.join(".agents").join("skills");
    match copy_tree(&SKILLS, &dest_root) {
        Ok(()) => eprintln!("[skills] bundled skills installed to {}", dest_root.display()),
        Err(e) => eprintln!("[skills] install failed: {e}"),
    }
}

/// Recursive copy mirroring the Wails WalkDir install: create dirs, write
/// files (fs::write overwrites). Entry names come from the entry's own path
/// file_name(), so nesting is preserved regardless of the embedded path
/// convention used by include_dir.
fn copy_tree(dir: &Dir, dest: &Path) -> io::Result<()> {
    fs::create_dir_all(dest)?;
    for entry in dir.entries() {
        match entry {
            DirEntry::Dir(d) => {
                let name = entry_name(d.path())?;
                copy_tree(d, &dest.join(name))?;
            }
            DirEntry::File(f) => {
                let name = entry_name(f.path())?;
                fs::write(dest.join(name), f.contents())?;
            }
        }
    }
    Ok(())
}

fn entry_name(path: &Path) -> io::Result<OsString> {
    path.file_name().map(OsString::from).ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            "embedded skills entry has no file name",
        )
    })
}

fn home_dir() -> Option<PathBuf> {
    for key in ["USERPROFILE", "HOME"] {
        if let Ok(v) = env::var(key) {
            if !v.is_empty() {
                return Some(PathBuf::from(v));
            }
        }
    }
    None
}
