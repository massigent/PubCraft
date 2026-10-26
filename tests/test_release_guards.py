"""Fast portable checks: `python3 -m unittest discover -s tests -p test_release_guards.py`."""
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
from macos_arch import architectures, require_universal
from check_macos_universal import deployment_versions
from check_release_version import validate


def thin(cpu):
    return struct.pack("<IIIIIIII", 0xFEEDFACF, cpu, 0, 2, 0, 0, 0, 0)


def fat(cpus):
    offset = 8 + 20 * len(cpus)
    table, slices = b"", b""
    for cpu in cpus:
        data = thin(cpu)
        table += struct.pack(">IIIII", cpu, 0, offset, len(data), 0)
        offset += len(data)
        slices += data
    return struct.pack(">II", 0xCAFEBABE, len(cpus)) + table + slices


class ArchitectureGuards(unittest.TestCase):
    def test_universal_passes(self):
        self.assertEqual(require_universal(fat([0x01000007, 0x0100000C])), {"x86_64", "arm64"})

    def test_deployment_version_ignores_linker_and_dylib_versions(self):
        text = ('Load command 1\n cmd LC_BUILD_VERSION\n minos 13.0\n sdk 26.2\n'
                ' ntools 1\n tool 3\n version 1230.1\n'
                'Load command 2\n cmd LC_LOAD_DYLIB\n current version 9000.0\n')
        self.assertEqual(deployment_versions(text), ['13.0'])
        self.assertEqual(deployment_versions('Load command 1\ncmd LC_VERSION_MIN_MACOSX\nversion 10.13\n'), ['10.13'])

    def test_thin_binaries_rejected(self):
        for cpu in [0x01000007, 0x0100000C]:
            with self.subTest(cpu=cpu), self.assertRaises(ValueError):
                require_universal(thin(cpu))

    def test_duplicate_slice_rejected(self):
        with self.assertRaises(ValueError):
            require_universal(fat([0x01000007, 0x01000007]))

    def test_truncated_or_mismatched_header_rejected(self):
        data = fat([0x01000007, 0x0100000C])
        for bad in [b"", data[:-1], data[:7], data[:8] + b"\xff" * 40 + data[48:]]:
            with self.subTest(length=len(bad)), self.assertRaises(ValueError):
                require_universal(bad)
        corrupt = bytearray(data)
        struct.pack_into("<I", corrupt, 48 + 4, 0x0100000C)
        with self.assertRaises(ValueError):
            architectures(corrupt)


class VersionGuards(unittest.TestCase):
    def fixture(self, root):
        files = {
            'VERSION': '1.0.0-alpha.3', 'BUILD_NUMBER': '3',
            'src/pubcraft.c': '#define VERSION "1.0.0-alpha.3"',
            'src/pubcraftgui/version.go': 'const appVersion = "1.0.0-alpha.3"',
            'src/installer.nsi': '!define PUBCRAFT_VERSION "1.0.0-alpha.3"',
            'src/pubcraftgui/web/index.html': 'data-i18n="welcome_version">Version 1.0.0-alpha.3</div>',
            'src/pubcraftgui/web/static/app.js': '"welcome_version": "Version 1.0.0-alpha.3"',
        }
        for name, data in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(data)

    def test_markers_must_all_agree(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            self.fixture(root)
            with patch('check_release_version.subprocess.run', return_value=SimpleNamespace(returncode=1)):
                self.assertEqual(validate(root), [])
                (root / 'src/pubcraftgui/version.go').write_text('const appVersion = "old"')
                self.assertTrue(any('version.go' in e for e in validate(root)))

    def test_build_number_must_increase_when_history_available(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            self.fixture(root)
            (root / 'BUILD_NUMBER').write_text('2')
            responses = [SimpleNamespace(returncode=0, stdout='v1.0.0-alpha.2\n'),
                         SimpleNamespace(returncode=0, stdout='2\n')]
            with patch('check_release_version.subprocess.run', side_effect=responses):
                self.assertTrue(any('must exceed 2' in e for e in validate(root)))


if __name__ == "__main__":
    unittest.main()
