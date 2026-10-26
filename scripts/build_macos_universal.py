#!/usr/bin/env python3
"""Build ALL shipped macOS executables as Universal 2; never reuse native outputs."""
import argparse
import os
from pathlib import Path
import platform
import shlex
import subprocess
import tempfile

from macos_arch import require_universal

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    print("+", " ".join(map(str, args)), flush=True)
    subprocess.run([str(a) for a in args], check=True, **kwargs)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--min-version", default="13.0")
    args = p.parse_args()
    if platform.system() != "Darwin":
        p.error("a macOS SDK and Apple signing tools are required")
    build = ROOT / "build"
    build.mkdir(exist_ok=True)
    sdk = subprocess.check_output(["xcrun", "--sdk", "macosx", "--show-sdk-path"], text=True).strip()
    cc = shlex.split(os.environ.get("CC", "clang"))
    flags = ["-arch", "x86_64", "-arch", "arm64", "-isysroot", sdk,
             "-mmacosx-version-min=" + args.min_version, "-O2"]
    run(*cc, *flags, "-std=c11", "-Wall", "-Wextra", ROOT / "src/pubcraft.c", "-o", build / "pubcraft")
    run(*cc, *flags, "-fobjc-arc", "-framework", "Cocoa",
        ROOT / "src/pubcraft-launcher.m", "-o", build / "pubcraft-launcher")
    run(*cc, *flags, "-fmodules", "-framework", "Cocoa", "-framework", "WebKit",
        ROOT / "src/pubcraft-html2pdf.m", "-o", build / "pubcraft-html2pdf")
    with tempfile.TemporaryDirectory(prefix="pubcraft-universal-") as d:
        slices = []
        for arch in ("amd64", "arm64"):
            dest = Path(d) / ("pubcraftgui-" + arch)
            env = dict(os.environ, CGO_ENABLED="0", GOOS="darwin", GOARCH=arch)
            run("go", "-C", ROOT / "src/pubcraftgui", "build", "-trimpath", "-o", dest, ".", env=env)
            slices.append(dest)
        run("lipo", "-create", *slices, "-output", build / "pubcraftgui")
    for name in ("pubcraft-launcher", "pubcraft", "pubcraftgui", "pubcraft-html2pdf"):
        path = build / name
        require_universal(path.read_bytes(), name)
        # ARM64 code must have a valid signature, including in portable archives.
        run("codesign", "--force", "--sign", "-", path)
        run("codesign", "--verify", "--strict", path)
    print("PASS: native launcher, CLI, GUI and PDF helper are Universal 2")


if __name__ == "__main__":
    main()
