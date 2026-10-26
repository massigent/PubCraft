#!/usr/bin/env python3
"""Verify that portable release archives contain the current binaries and fonts."""
import hashlib
from pathlib import Path
import sys
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'VERSION').read_text().strip()
PACKAGES = {
    f'PubCraft-{VERSION}-macos.tar.gz': ('pubcraft', 'pubcraftgui'),
    f'PubCraft-{VERSION}-linux-x64.tar.gz': ('pubcraft-linux-amd64', 'pubcraftgui-linux-amd64'),
    f'PubCraft-{VERSION}-windows-x64.zip': ('pubcraft-windows-amd64.exe', 'pubcraftgui-windows-amd64.exe'),
}

def verify(path):
    if path.name not in PACKAGES:
        raise ValueError('Formato pacchetto non supportato: ' + path.name)
    if path.suffix == '.zip':
        with zipfile.ZipFile(path) as archive:
            files = {n.lstrip('./'): archive.read(n) for n in archive.namelist() if not n.endswith('/')}
    else:
        with tarfile.open(path) as archive:
            files = {m.name.lstrip('./'): archive.extractfile(m).read() for m in archive.getmembers() if m.isfile()}
    windows = path.suffix == '.zip'
    for source, packaged in zip(PACKAGES[path.name], ('pubcraft.exe', 'pubcraftgui.exe') if windows else ('pubcraft', 'pubcraftgui')):
        binary = ROOT / 'build' / source
        if not binary.exists():
            raise ValueError('Binario corrente mancante: ' + str(binary))
        if hashlib.sha256(files.get(packaged, b'')).digest() != hashlib.sha256(binary.read_bytes()).digest():
            raise ValueError('Binario non aggiornato nel pacchetto: ' + packaged)
        if source == PACKAGES[path.name][0]:
            sources = [ROOT/'src/pubcraft.c']
        else:
            sources = list((ROOT/'src/pubcraftgui').glob('*.go')) + list((ROOT/'src/pubcraftgui/web').rglob('*'))
        if any(p.is_file() and p.stat().st_mtime > binary.stat().st_mtime for p in sources):
            raise ValueError('Ricompilare il binario dai sorgenti attuali: ' + source)
    for font in (ROOT/'fonts').iterdir():
        if font.is_file() and files.get('fonts/' + font.name) != font.read_bytes():
            raise ValueError('Risorsa font assente/non aggiornata: ' + font.name)
    print('PASS:', path.name)

if __name__ == '__main__':
    args = sys.argv[1:] or [str(ROOT/'build'/name) for name in PACKAGES]
    failed = False
    for arg in args:
        try:
            verify(Path(arg))
        except (OSError, ValueError, tarfile.TarError, zipfile.BadZipFile) as error:
            failed = True
            print('FAIL:', arg, '-', error, file=sys.stderr)
    sys.exit(1 if failed else 0)
