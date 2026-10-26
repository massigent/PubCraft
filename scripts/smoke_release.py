#!/usr/bin/env python3
"""Native CLI/GUI smoke tests using only synthetic data and an isolated home."""
import argparse
import json
import os
import plistlib
import platform
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--cli", type=Path, required=True)
    p.add_argument("--gui", type=Path, required=True)
    p.add_argument("--expected-arch", choices=("amd64", "arm64"))
    args = p.parse_args()
    cli, gui = args.cli.resolve(), args.gui.resolve()
    prefix = []
    if args.expected_arch:
        if platform.system() != "Darwin":
            p.error("explicit native architecture checks currently target macOS")
        native = {"amd64": "x86_64", "arm64": "arm64"}[args.expected_arch]
        if platform.machine() != native:
            p.error(f"native {native} test cannot run on {platform.machine()}")
        prefix = ["/usr/bin/arch", "-" + native]

    def command(binary, *rest):
        return prefix + [str(binary), *map(str, rest)]
    with tempfile.TemporaryDirectory(prefix="pubcraft-smoke-") as d:
        tmp = Path(d)
        home = tmp / "home"
        home.mkdir()
        env = dict(os.environ, HOME=str(home), USERPROFILE=str(home),
                   PUBCRAFT_CONFIG_DIR=str(home / "config"), PUBCRAFTGUI_NO_BROWSER="1",
                   PUBCRAFT_BIN=str(cli))
        vault = tmp / "book with spaces"
        subprocess.run(command(cli, "init", vault, "Release: synthetic test"), env=env, check=True)
        # Exercise the native archive backend and XML sanitizer, not just startup.
        (vault / "01-capitolo-1.md").write_bytes(
            b'---\ntitle: Chapter\n---\n\nText \x0c \x0b caf\xe8, emoji \xf0\x9f\x98\x80.\n\n- Outer\n    - Inner\n')
        epub = tmp / "book.epub"
        subprocess.run(command(cli, "build", vault, epub), env=env, check=True)
        with zipfile.ZipFile(epub) as z:
            assert z.infolist()[0].filename == "mimetype"
            assert z.infolist()[0].compress_type == zipfile.ZIP_STORED
            assert z.read("mimetype") == b"application/epub+zip"
            assert z.testzip() is None
            for name in z.namelist():
                if name.endswith((".xml", ".opf", ".xhtml")):
                    ET.fromstring(z.read(name))
        # A minimal synthetic archive also exercises the native extraction path.
        archive = tmp / "synthetic archive.vellum"
        with zipfile.ZipFile(archive, "w") as z:
            z.writestr("bookMetadata.plist", plistlib.dumps({"title": "Synthetic archive", "author": "Test"}))
            z.writestr("content.vellumcontent", plistlib.dumps({"sample": "content"}, fmt=plistlib.FMT_BINARY))
        inspected = subprocess.run(command(cli, "info", archive), env=env, capture_output=True, text=True, check=True)
        assert "Synthetic archive" in inspected.stdout
        proc = subprocess.Popen(command(gui, vault), env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            descriptor = home / "config/instance.json"
            deadline = time.monotonic() + 30
            while not descriptor.exists():
                if proc.poll() is not None:
                    raise RuntimeError(f"GUI exited before startup: {proc.returncode}")
                if time.monotonic() > deadline:
                    raise TimeoutError("GUI instance descriptor missing")
                time.sleep(0.1)
            instance = json.loads(descriptor.read_text())
            url = instance["url"]
            with urllib.request.urlopen(url + "/api/state", timeout=10) as resp:
                state = json.load(resp)
            assert state["book"]["title"] == "Release: synthetic test", state
            assert len(state["book"]["chapters"]) == 1
            if args.expected_arch:
                request = urllib.request.Request(url + "/api/diagnostics/report",
                    data=json.dumps({"description": "Synthetic native architecture check"}).encode(),
                    headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(request, timeout=10) as resp:
                    report = json.load(resp)
                assert report["os_arch"] == "darwin/" + args.expected_arch, report["os_arch"]
                print("PASS: running GUI architecture", report["os_arch"])
            print("PASS: native CLI EPUB and GUI startup/state")
        finally:
            proc.terminate()
            proc.wait(timeout=15)


if __name__ == "__main__":
    main()
