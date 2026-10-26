#!/usr/bin/env python3
"""Set generated app metadata consistently on Intel and ARM build hosts."""
from pathlib import Path
import plistlib
import sys

ROOT = Path(__file__).resolve().parents[1]
app = Path(sys.argv[1])
minimum = sys.argv[2]
version = (ROOT / "VERSION").read_text().strip()
build = (ROOT / "BUILD_NUMBER").read_text().strip()
p = app / "Contents/Info.plist"
info = {
    "CFBundleExecutable": "pubcraft-launcher", "CFBundleName": "PubCraft",
    "CFBundleDisplayName": "PubCraft", "CFBundleIdentifier": "com.pubcraft.app",
    "CFBundlePackageType": "APPL", "CFBundleInfoDictionaryVersion": "6.0",
    "CFBundleIconFile": "droplet.icns", "LSUIElement": True,
    "NSHighResolutionCapable": True,
    "CFBundleDocumentTypes": [{"CFBundleTypeName": "PubCraft project or manuscript",
        "CFBundleTypeRole": "Viewer", "LSHandlerRank": "Alternate",
        "LSItemContentTypes": ["public.item", "public.folder"]}],
}
info.update(CFBundleShortVersionString=version.split("-", 1)[0],
            CFBundleVersion=build, CFBundleGetInfoString=version,
            LSMinimumSystemVersion=minimum,
            LSMinimumSystemVersionByArchitecture={"x86_64": minimum, "arm64": minimum})
p.write_bytes(plistlib.dumps(info, sort_keys=False))
