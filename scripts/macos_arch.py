"""Portable Mach-O architecture checks (no lipo required for archive inspection)."""
import struct

CPUS = {0x01000007: "x86_64", 0x0100000C: "arm64"}
REQUIRED = {"x86_64", "arm64"}
THIN = {b"\xcf\xfa\xed\xfe": "<", b"\xfe\xed\xfa\xcf": ">"}
FAT = {b"\xca\xfe\xba\xbe": (">", False), b"\xbe\xba\xfe\xca": ("<", False),
       b"\xca\xfe\xba\xbf": (">", True), b"\xbf\xba\xfe\xca": ("<", True)}


def architectures(data):
    magic = bytes(data[:4])
    if magic in THIN:
        if len(data) < 32:
            raise ValueError("truncated Mach-O header")
        cpu = struct.unpack_from(THIN[magic] + "I", data, 4)[0]
        return {CPUS.get(cpu, hex(cpu))}
    if magic not in FAT or len(data) < 8:
        raise ValueError("not a supported Mach-O binary")
    endian, wide = FAT[magic]
    count = struct.unpack_from(endian + "I", data, 4)[0]
    size = 32 if wide else 20
    if not 1 <= count <= 16 or len(data) < 8 + size * count:
        raise ValueError("invalid universal Mach-O table")
    result, spans = set(), []
    for i in range(count):
        at = 8 + i * size
        cpu, _ = struct.unpack_from(endian + "II", data, at)
        offset, length = struct.unpack_from(endian + ("QQ" if wide else "II"), data, at + 8)
        name = CPUS.get(cpu, hex(cpu))
        if name in result or length < 32 or offset < 8 + size * count or offset + length > len(data):
            raise ValueError("invalid universal Mach-O slice")
        if any(offset < end and start < offset + length for start, end in spans):
            raise ValueError("overlapping universal Mach-O slices")
        if bytes(data[offset:offset + 4]) not in THIN:
            raise ValueError("universal slice must be a thin Mach-O, not a nested container")
        if architectures(data[offset:offset + length]) != {name}:
            raise ValueError("universal table does not match slice CPU")
        result.add(name)
        spans.append((offset, offset + length))
    return result


def require_universal(data, label="binary"):
    found = architectures(data)
    if found != REQUIRED:
        raise ValueError(f"{label}: expected Universal 2 (arm64 + x86_64), found {sorted(found)}")
    return found
