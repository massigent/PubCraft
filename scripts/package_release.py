#!/usr/bin/env python3
"""Package freshly built binaries; never reuse artifacts from another release.

Run on the target OS. macOS bundles are assembled/signed by `make pkg-macos`.
Linux AppImages require verified APPIMAGETOOL and APPIMAGE_RUNTIME paths.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / "VERSION").read_text().strip()
BUILD = ROOT / "build"
IGNORE = shutil.ignore_patterns("._*", ".DS_Store", "__pycache__")


def run(*args, **kwargs):
    subprocess.run([str(a) for a in args], check=True, **kwargs)


def copy_file(src, dst, executable=False):
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(src, dst)
    dst.chmod(0o755 if executable else 0o644)


def resources(stage, windows=False):
    for name in ("README.md", "README.it.md", "LICENSE", "VERSION"):
        dest = "README.txt" if windows and name == "README.md" else name
        copy_file(ROOT / name, stage / dest)
    for name in ("fonts", "third-party-licenses"):
        shutil.copytree(ROOT / name, stage / name, ignore=IGNORE)
        for p in (stage / name).rglob("*"):
            p.chmod(0o755 if p.is_dir() else 0o644)


def portable(stage, windows=False):
    for source, name in (("pubcraft", "pubcraft"), ("pubcraftgui", "pubcraftgui")):
        suffix = "-windows-amd64.exe" if windows else "-linux-amd64"
        copy_file(BUILD / (source + suffix), stage / (name + (".exe" if windows else "")), True)
    resources(stage, windows)
    if windows:
        copy_file(ROOT / "src/app.ico", stage / "app.ico")
        (stage / "Avvia-PubCraft.bat").write_bytes(b'@echo off\r\nstart "" "%~dp0pubcraftgui.exe"\r\n')
        out = BUILD / f"PubCraft-{VERSION}-windows-x64.zip"
        with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
            for p in sorted(stage.rglob("*")):
                if p.is_file():
                    z.write(p, p.relative_to(stage).as_posix())
    else:
        for name in ("install.sh", "pubcraft.desktop", "pubcraft.png"):
            copy_file(ROOT / "src" / name, stage / name, name.endswith(".sh"))
        out = BUILD / f"PubCraft-{VERSION}-linux-x64.tar.gz"
        def metadata(info):
            info.uid = info.gid = 0
            info.uname = info.gname = "root"
            return info
        with tarfile.open(out, "w:gz") as t:
            for p in sorted(stage.iterdir()):
                t.add(p, arcname=p.name, filter=metadata)
    print(out)


def linux_packages(tmp):
    stage = tmp / "portable"
    stage.mkdir()
    portable(stage)
    pkg = tmp / "deb"
    (pkg / "DEBIAN").mkdir(parents=True)
    deb_version = VERSION.replace("-", "~", 1)
    (pkg / "DEBIAN/control").write_text(
        f"Package: pubcraft\nVersion: {deb_version}\nSection: text\nPriority: optional\n"
        "Architecture: amd64\nMaintainer: PubCraft Contributors\nDepends: zip, unzip\n"
        "Recommends: chromium\nHomepage: https://github.com/massigent/PubCraft\n"
        "Description: Local Markdown book editor and EPUB/print publisher\n")
    for name in ("pubcraft", "pubcraftgui"):
        copy_file(stage / name, pkg / "usr/bin" / name, True)
    shutil.copytree(stage / "fonts", pkg / "usr/share/pubcraft/fonts")
    for name in ("README.md", "README.it.md", "LICENSE"):
        copy_file(stage / name, pkg / "usr/share/doc/pubcraft" / name)
    shutil.copytree(stage / "third-party-licenses", pkg / "usr/share/doc/pubcraft/third-party-licenses")
    copy_file(ROOT / "src/pubcraft.desktop", pkg / "usr/share/applications/pubcraft.desktop")
    copy_file(ROOT / "src/pubcraft.png", pkg / "usr/share/icons/hicolor/256x256/apps/pubcraft.png")
    run("dpkg-deb", "--root-owner-group", "--build", pkg, BUILD / f"PubCraft-{VERSION}-linux-amd64.deb")

    tool = Path(os.environ["APPIMAGETOOL"]).resolve()
    runtime = Path(os.environ["APPIMAGE_RUNTIME"]).resolve()
    app = tmp / "PubCraft.AppDir"
    for name in ("pubcraft", "pubcraftgui"):
        copy_file(stage / name, app / "usr/bin" / name, True)
    shutil.copytree(stage / "fonts", app / "usr/share/pubcraft/fonts")
    shutil.copytree(stage / "third-party-licenses", app / "usr/share/doc/pubcraft/third-party-licenses")
    for name in ("pubcraft.desktop", "pubcraft.png"):
        copy_file(ROOT / "src" / name, app / name)
    (app / "AppRun").write_text('#!/bin/sh\nset -eu\nHERE="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"\nexport PATH="$HERE/usr/bin:$PATH"\nexec "$HERE/usr/bin/pubcraftgui" "$@"\n')
    (app / "AppRun").chmod(0o755)
    env = dict(os.environ, ARCH="x86_64", APPIMAGE_EXTRACT_AND_RUN="1")
    run(tool, "--runtime-file", runtime, app, BUILD / f"PubCraft-{VERSION}-x86_64.AppImage", env=env)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("platform", choices=("linux", "windows"))
    args = parser.parse_args()
    BUILD.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="pubcraft-package-") as d:
        tmp = Path(d)
        if args.platform == "linux":
            linux_packages(tmp)
        else:
            stage = tmp / "portable"
            stage.mkdir()
            portable(stage, True)
            run("makensis", "installer.nsi", cwd=ROOT / "src")


if __name__ == "__main__":
    main()
