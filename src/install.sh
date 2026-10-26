#!/bin/sh
# Portable archive installer. Resolve resources relative to this script, not cwd.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PREFIX="${PREFIX:-/usr/local}"
install -d "$PREFIX/bin" "$PREFIX/share/pubcraft/fonts"
install -m 755 pubcraft "$PREFIX/bin/"
install -m 755 pubcraftgui "$PREFIX/bin/"
if [ -f pubcraft-html2pdf ]; then install -m 755 pubcraft-html2pdf "$PREFIX/bin/"; fi
if [ -d fonts ]; then cp -R fonts/. "$PREFIX/share/pubcraft/fonts/"; fi
if [ -d third-party-licenses ]; then
  install -d "$PREFIX/share/doc/pubcraft"
  cp -R third-party-licenses "$PREFIX/share/doc/pubcraft/"
fi
if [ -f pubcraft.desktop ]; then
 install -d "$PREFIX/share/applications"
 install -m 644 pubcraft.desktop "$PREFIX/share/applications/"
fi
if [ -f pubcraft.png ]; then
 install -d "$PREFIX/share/icons/hicolor/256x256/apps"
 install -m 644 pubcraft.png "$PREFIX/share/icons/hicolor/256x256/apps/"
fi
printf 'PubCraft installato in %s\n' "$PREFIX"
