#!/usr/bin/env python3
"""Fail closed unless the app AND every macOS download contain Universal 2 code.

Native --smoke checks must run once on Intel and once on Apple Silicon against
artifacts downloaded from the SAME build, not independently rebuilt packages.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import signal
import subprocess
import time
import urllib.request
import tarfile
import tempfile

from macos_arch import require_universal

ROOT = Path(__file__).resolve().parents[1]
PROGRAMS = ("pubcraft", "pubcraftgui", "pubcraft-html2pdf")


def run(*args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def deployment_versions(load_commands):
    versions = []
    for command in re.split(r"Load command \d+", load_commands):
        if "LC_BUILD_VERSION" in command:
            versions += re.findall(r"^\s*minos\s+(\d+\.\d+(?:\.\d+)?)", command, re.MULTILINE)
        elif "LC_VERSION_MIN_MACOSX" in command:
            versions += re.findall(r"^\s*version\s+(\d+\.\d+(?:\.\d+)?)", command, re.MULTILINE)
    return versions


def check_binary(path):
    require_universal(path.read_bytes(), str(path))
    # Independently ask Apple's tools as well as our portable header parser.
    found = set(subprocess.check_output(["lipo", "-archs", str(path)], text=True).split())
    if found != {"x86_64", "arm64"}:
        raise ValueError(f"lipo rejected the Universal 2 contract: {path}: {found}")
    run("codesign", "--verify", "--strict", path)
    for arch in ("x86_64", "arm64"):
        load = subprocess.check_output(["otool", "-arch", arch, "-l", str(path)], text=True)
        # Linker/tool versions in LC_BUILD_VERSION are not minimum OS versions.
        versions = deployment_versions(load)
        if not versions:
            raise ValueError(f"missing minimum OS metadata: {path} ({arch})")
        for v in versions:
            if tuple(map(int, v.split(".")[:2])) > (13, 0):
                raise ValueError(f"{path} ({arch}) silently requires macOS {v}, contract is 13.0")


def check_bundle(app, version, public=False):
    info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
    if public:
        for path in [app, *app.rglob("*")]:
            mode = path.stat().st_mode
            needed = 0o555 if path.is_dir() or path.parent == app / "Contents/MacOS" else 0o444
            if mode & needed != needed:
                raise ValueError(f"distributed app has owner-only/inaccessible permissions: {path}")
    if info.get("LSMinimumSystemVersion") != "13.0":
        raise ValueError("app must declare the documented macOS 13.0 minimum")
    for arch in ("x86_64", "arm64"):
        if info.get("LSMinimumSystemVersionByArchitecture", {}).get(arch) != "13.0":
            raise ValueError(f"missing/incorrect app minimum OS for {arch}")
    if info.get("CFBundleGetInfoString") != version:
        raise ValueError("app release version does not match VERSION")
    if info.get("CFBundleExecutable") != "pubcraft-launcher":
        raise ValueError("app must use the native Universal pubcraft-launcher, not a system applet or CLI")
    directory = app / "Contents/MacOS"
    required = set(PROGRAMS) | {info["CFBundleExecutable"]}
    for name in required:
        if not (directory / name).is_file():
            raise ValueError(f"missing app executable: {name}")
    for p in directory.rglob("*"):
        if p.is_file():
            check_binary(p)
    run("codesign", "--verify", "--deep", "--strict", app)
    return {name: hashlib.sha256((directory / name).read_bytes()).hexdigest() for name in sorted(required)}


def one_app(directory):
    apps = list(directory.rglob("PubCraft.app"))
    if len(apps) != 1:
        raise ValueError(f"expected exactly one PubCraft.app in {directory}, found {len(apps)}")
    return apps[0]


def verify_source(gui, expected, tmp):
    for arch in ("x86_64", "arm64"):
        path = tmp / ("gui-" + arch)
        run("lipo", gui, "-thin", arch, "-output", path)
        text = subprocess.check_output(["go", "version", "-m", str(path)], text=True)
        if "vcs.revision=" + expected not in text or "vcs.modified=false" not in text:
            raise ValueError(f"GUI slice {arch} not built from clean commit {expected}")


def smoke_launcher(app, native, expected_go_arch, tmp):
    home = tmp / "launcher-home"
    home.mkdir()
    config = home / "config"
    env = dict(os.environ, HOME=str(home), PUBCRAFT_CONFIG_DIR=str(config), PUBCRAFTGUI_NO_BROWSER="1")
    proc = subprocess.Popen(["/usr/bin/arch", "-" + native, str(app / "Contents/MacOS/pubcraft-launcher")], env=env)
    instance = None
    try:
        descriptor = config / "instance.json"
        deadline = time.monotonic() + 30
        while not descriptor.exists():
            if time.monotonic() > deadline:
                raise TimeoutError("native .app launcher did not start its GUI")
            time.sleep(0.1)
        instance = json.loads(descriptor.read_text())
        request = urllib.request.Request(instance["url"] + "/api/diagnostics/report",
            data=json.dumps({"description": "Synthetic app-launcher architecture check"}).encode(),
            headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=10) as response:
            report = json.load(response)
        if report["os_arch"] != "darwin/" + expected_go_arch:
            raise ValueError("app launcher started a translated/wrong-architecture GUI")
        proc.wait(timeout=15)
        print("PASS: real .app launcher started native", report["os_arch"])
    finally:
        if instance:
            try:
                os.kill(instance["pid"], signal.SIGTERM)
            except ProcessLookupError:
                pass
        if proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=10)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--app", type=Path)
    p.add_argument("--app-zip", type=Path)
    p.add_argument("--portable", type=Path)
    p.add_argument("--dmg", type=Path)
    p.add_argument("--pkg", type=Path)
    p.add_argument("--expected-source")
    p.add_argument("--native-arch", choices=("amd64", "arm64"))
    p.add_argument("--smoke", action="store_true")
    p.add_argument("--report", type=Path)
    args = p.parse_args()
    if platform.system() != "Darwin":
        p.error("signature/package checks require macOS; portable architecture checks live in tests/check_packages.py")
    version = (ROOT / "VERSION").read_text().strip()
    expected_host = {"amd64": "x86_64", "arm64": "arm64"}.get(args.native_arch)
    if expected_host and platform.machine() != expected_host:
        p.error(f"native test needs {expected_host}, this Python process is {platform.machine()}")
    if args.smoke and not args.native_arch:
        p.error("--smoke requires --native-arch so an emulated run cannot be mistaken for native testing")
    with tempfile.TemporaryDirectory(prefix="pubcraft-macos-verify-") as d:
        tmp = Path(d)
        zip_app = None
        if args.app_zip:
            run("ditto", "-x", "-k", args.app_zip.resolve(), tmp / "zip")
            zip_app = one_app(tmp / "zip")
        app = args.app.resolve() if args.app else zip_app
        if app is None:
            p.error("supply --app or --app-zip")
        reference = check_bundle(app, version, public=not bool(args.app))
        if zip_app and check_bundle(zip_app, version, public=True) != reference:
            raise ValueError("app ZIP does not contain the same executables as the reference app")
        if args.expected_source:
            verify_source(app / "Contents/MacOS/pubcraftgui", args.expected_source, tmp)
        if args.portable:
            with tarfile.open(args.portable) as archive:
                archive.extractall(tmp / "portable", filter="data")
            for name in PROGRAMS:
                path = tmp / "portable" / name
                check_binary(path)
                if hashlib.sha256(path.read_bytes()).hexdigest() != reference[name]:
                    raise ValueError(f"portable archive has different {name}")
        if args.pkg:
            run("pkgutil", "--expand-full", args.pkg.resolve(), tmp / "pkg")
            if check_bundle(one_app(tmp / "pkg"), version, public=True) != reference:
                raise ValueError("PKG contains different app executables")
        if args.dmg:
            mount = tmp / "mount"
            mount.mkdir()
            run("hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, args.dmg.resolve())
            try:
                if check_bundle(mount / "PubCraft.app", version, public=True) != reference:
                    raise ValueError("DMG contains different app executables")
            finally:
                run("hdiutil", "detach", mount)
        if args.smoke:
            binary_dir = app / "Contents/MacOS"
            smoke_launcher(app, expected_host, args.native_arch, tmp)
            run("python3", ROOT / "scripts/smoke_release.py", "--cli", binary_dir / "pubcraft",
                "--gui", binary_dir / "pubcraftgui", "--expected-arch", args.native_arch)
            html, pdf = tmp / "sample.html", tmp / "sample.pdf"
            html.write_text("<!doctype html><html><body><p>Synthetic native PDF check.</p></body></html>")
            run("/usr/bin/arch", "-" + expected_host, binary_dir / "pubcraft-html2pdf", html, pdf, timeout=90)
            if not pdf.read_bytes().startswith(b"%PDF-"):
                raise ValueError("native PDF helper produced invalid/empty output")
        report = {"version": version, "required_architectures": ["x86_64", "arm64"],
                  "host": platform.machine(), "native_smoke": bool(args.smoke),
                  "source_commit": args.expected_source, "executables_sha256": reference,
                  "packages_checked": [n for n in ("app_zip", "portable", "dmg", "pkg") if getattr(args, n)]}
        if args.report:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(json.dumps(report, indent=2) + "\n")
        print("PASS: Universal 2 app/packages", json.dumps(report))


if __name__ == "__main__":
    main()
