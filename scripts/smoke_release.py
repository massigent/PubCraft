#!/usr/bin/env python3
"""Native CLI/GUI smoke tests using only synthetic data and an isolated home."""
import argparse
import json
import os
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
    args = p.parse_args()
    cli, gui = args.cli.resolve(), args.gui.resolve()
    with tempfile.TemporaryDirectory(prefix="pubcraft-smoke-") as d:
        tmp = Path(d)
        home = tmp / "home"
        home.mkdir()
        env = dict(os.environ, HOME=str(home), USERPROFILE=str(home),
                   PUBCRAFT_CONFIG_DIR=str(home / "config"), PUBCRAFTGUI_NO_BROWSER="1",
                   PUBCRAFT_BIN=str(cli))
        vault = tmp / "book with spaces"
        subprocess.run([str(cli), "init", str(vault), "Release: synthetic test"], env=env, check=True)
        epub = tmp / "book.epub"
        subprocess.run([str(cli), "build", str(vault), str(epub)], env=env, check=True)
        with zipfile.ZipFile(epub) as z:
            assert z.infolist()[0].filename == "mimetype"
            assert z.infolist()[0].compress_type == zipfile.ZIP_STORED
            assert z.read("mimetype") == b"application/epub+zip"
            assert z.testzip() is None
            for name in z.namelist():
                if name.endswith((".xml", ".opf", ".xhtml")):
                    ET.fromstring(z.read(name))
        proc = subprocess.Popen([str(gui), str(vault)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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
            print("PASS: native CLI EPUB and GUI startup/state")
        finally:
            proc.terminate()
            proc.wait(timeout=15)


if __name__ == "__main__":
    main()
