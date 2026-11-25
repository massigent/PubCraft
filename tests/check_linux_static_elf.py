#!/usr/bin/env python3
"""Fail-closed ELF portability guard for Linux release binaries.

Verifies that each given ELF executable is fully static for the target
architecture: no ELF interpreter (PT_INTERP) and no dynamic dependencies
(DT_NEEDED). A musl/glibc-linked dynamic GUI would pass compilation but fail
on hosts without the matching loader, so this guard runs before any smoke
test or packaging step. Uses `readelf` via checked subprocess calls only.
"""
import argparse
import re
import subprocess
import sys
from pathlib import Path

EXPECTED_MACHINE = {"amd64": "Advanced Micro Devices X86-64", "arm64": "AArch64"}


def readelf(path: Path, *flags: str) -> str:
    """Run readelf with a checked (non-masking) subprocess call."""
    proc = subprocess.run(
        ["readelf", *flags, str(path)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
        env={"LC_ALL": "C", "LANG": "C", "PATH": "/usr/bin:/bin:/usr/local/bin"},
    )
    if proc.returncode != 0:
        raise SystemExit(
            f"FAIL: readelf returned {proc.returncode} on {path}: {proc.stderr.strip()}"
        )
    return proc.stdout


def check_static(path: Path, arch: str) -> None:
    if not path.is_file():
        raise SystemExit(f"FAIL: missing binary: {path}")

    # Reject non-ELF files early (macOS Mach-O, scripts, etc.).
    if not path.read_bytes()[:4] == b"\x7fELF":
        raise SystemExit(f"FAIL: {path} is not an ELF binary")

    machine_line = readelf(path, "-h")
    match = re.search(r"Machine:\s+(.+)", machine_line)
    if not match or match.group(1).strip() != EXPECTED_MACHINE[arch]:
        raise SystemExit(
            f"FAIL: {path} has unexpected machine type "
            f"'{match.group(1).strip() if match else '?'}' "
            f"(expected '{EXPECTED_MACHINE[arch]}' for {arch})"
        )

    program_headers = readelf(path, "-l")
    if re.search(r"INTERP\b", program_headers):
        raise SystemExit(
            f"FAIL: {path} is dynamically linked (PT_INTERP interpreter present). "
            "Linux release executables must be fully static ELF with no interpreter."
        )

    dynamic_section = readelf(path, "-d")
    needed = re.findall(r"Shared library: \[([^\]]+)\]", dynamic_section)
    if needed:
        raise SystemExit(
            f"FAIL: {path} declares DT_NEEDED dependencies {needed}. "
            "Linux release executables must be fully static ELF with no shared "
            "library dependencies."
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--arch",
        choices=sorted(EXPECTED_MACHINE),
        default="amd64",
        help="expected ELF machine architecture",
    )
    parser.add_argument("binaries", nargs="+", type=Path)
    args = parser.parse_args()
    for binary in args.binaries:
        check_static(binary, args.arch)
        print(f"OK: {binary} is a fully static {args.arch} ELF (no PT_INTERP, no DT_NEEDED)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
