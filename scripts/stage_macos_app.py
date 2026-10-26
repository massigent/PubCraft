#!/usr/bin/env python3
"""Copy a signed app to disposable staging and normalize public file modes.

External/test volumes can report owner-only modes. Do not preserve those modes
in a DMG/PKG/app ZIP intended for other users. File bytes/signatures stay intact.
"""
from pathlib import Path
import shutil
import sys

source, dest = map(Path, sys.argv[1:3])
shutil.copytree(source, dest, ignore=shutil.ignore_patterns("._*", ".DS_Store"))
for p in [dest, *dest.rglob("*")]:
    if p.is_symlink():
        raise ValueError(f"unexpected symlink in generated app: {p}")
    p.chmod(0o755 if p.is_dir() else 0o644)
for p in (dest / "Contents/MacOS").iterdir():
    if p.is_file():
        p.chmod(0o755)
