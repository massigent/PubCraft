#!/usr/bin/env python3
"""Check live version markers; historical release notes must never be rewritten."""
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def validate(root=ROOT):
    errors = []
    version = (root / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[A-Za-z0-9.]+)?", version):
        errors.append("VERSION is not a supported semantic version")
    build = (root / "BUILD_NUMBER").read_text(encoding="utf-8").strip()
    if not build.isdigit() or int(build) < 1:
        errors.append("BUILD_NUMBER must be a positive, monotonically increasing integer")
    # Full-history release checkouts must increase the build number. Source
    # archives/shallow development checkouts may have no prior tag to inspect.
    try:
        previous = subprocess.run(["git", "describe", "--tags", "--match", "v*",
            "--exclude", "v" + version, "--abbrev=0", "HEAD"], cwd=root,
            capture_output=True, text=True)
    except FileNotFoundError:
        previous = None
    if previous is not None and previous.returncode == 0 and build.isdigit():
        tag = previous.stdout.strip()
        old = subprocess.run(["git", "show", tag + ":BUILD_NUMBER"], cwd=root,
                             capture_output=True, text=True)
        if old.returncode == 0 and old.stdout.strip().isdigit():
            old_build = int(old.stdout.strip())
        else:
            legacy = re.search(r"-alpha\.(\d+)$", tag)
            old_build = int(legacy.group(1)) if legacy else 0
        if int(build) <= old_build:
            errors.append(f"BUILD_NUMBER must exceed {old_build} from {tag}")
    markers = {
        "src/pubcraft.c": '#define VERSION "' + version + '"',
        "src/pubcraftgui/version.go": 'const appVersion = "' + version + '"',
        "src/installer.nsi": '!define PUBCRAFT_VERSION "' + version + '"',
    }
    for name, expected in markers.items():
        if expected not in (root / name).read_text(encoding="utf-8"):
            errors.append(name + ": release version differs from VERSION")
    html = (root / "src/pubcraftgui/web/index.html").read_text(encoding="utf-8")
    if 'data-i18n="welcome_version">Version ' + version + '</div>' not in html:
        errors.append("welcome HTML version differs from VERSION")
    js = (root / "src/pubcraftgui/web/static/app.js").read_text(encoding="utf-8")
    welcome = re.findall(r'"welcome_version":\s*"([^"]+)"', js)
    if not welcome or any(not value.endswith(" " + version) for value in welcome):
        errors.append("one or more localized welcome versions differ from VERSION")
    return errors


def main():
    errors = validate()
    if errors:
        print("\n".join("FAIL: " + e for e in errors), file=sys.stderr)
        raise SystemExit(1)
    print("PASS: release version markers match VERSION and BUILD_NUMBER")


if __name__ == "__main__":
    main()
