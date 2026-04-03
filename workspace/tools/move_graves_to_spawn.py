#!/usr/bin/env python3

import argparse
import base64
import copy
import gzip
import hashlib
import io
import json
import math
import os
import struct
import time
import urllib.parse
import urllib.request
import zlib
from dataclasses import dataclass
from pathlib import Path


TAG_END = 0
TAG_BYTE = 1
TAG_SHORT = 2
TAG_INT = 3
TAG_LONG = 4
TAG_FLOAT = 5
TAG_DOUBLE = 6
TAG_BYTE_ARRAY = 7
TAG_STRING = 8
TAG_LIST = 9
TAG_COMPOUND = 10
TAG_INT_ARRAY = 11
TAG_LONG_ARRAY = 12


PASSABLE_BLOCK_IDS = {
    0, 6, 8, 9, 10, 11, 31, 32, 37, 38, 39, 40, 50, 51, 55, 59, 63, 65, 66,
    68, 69, 70, 72, 75, 76, 77, 78, 83, 93, 94, 104, 105, 106, 111, 115, 119,
    127, 131, 141, 142, 143, 147, 148, 149, 150, 157, 171, 175
}
UNSAFE_SUPPORT_BLOCK_IDS = {0, 8, 9, 10, 11}

KNOWN_GRAVES = [
    {"dim": 0, "region_path": "world/region/r.0.-1.mca", "x": 336, "y": 74, "z": -356},
    {"dim": 0, "region_path": "world/region/r.0.-1.mca", "x": 329, "y": 50, "z": -319},
    {"dim": 0, "region_path": "world/region/r.0.-1.mca", "x": 401, "y": 10, "z": -314},
    {"dim": 0, "region_path": "world/region/r.0.-2.mca", "x": 175, "y": 62, "z": -815},
    {"dim": 0, "region_path": "world/region/r.1.-1.mca", "x": 534, "y": 61, "z": -486},
    {"dim": 0, "region_path": "world/region/r.1.-1.mca", "x": 711, "y": 69, "z": -304},
    {"dim": 0, "region_path": "world/region/r.1.-1.mca", "x": 518, "y": 62, "z": -226},
    {"dim": 0, "region_path": "world/region/r.1.-1.mca", "x": 652, "y": 70, "z": -218},
    {"dim": 0, "region_path": "world/region/r.1.-2.mca", "x": 558, "y": 39, "z": -745},
    {"dim": 0, "region_path": "world/region/r.1.2.mca", "x": 644, "y": 72, "z": 1305},
    {"dim": -1, "region_path": "world/DIM-1/region/r.-1.-1.mca", "x": -2, "y": 31, "z": -221},
    {"dim": -1, "region_path": "world/DIM-1/region/r.-1.-1.mca", "x": -62, "y": 59, "z": -129},
    {"dim": -1, "region_path": "world/DIM-1/region/r.-1.-1.mca", "x": -30, "y": 60, "z": -82},
    {"dim": -1, "region_path": "world/DIM-1/region/r.-1.0.mca", "x": -32, "y": 99, "z": 135},
    {"dim": -1, "region_path": "world/DIM-1/region/r.0.-1.mca", "x": 45, "y": 37, "z": -94},
    {"dim": -1, "region_path": "world/DIM-1/region/r.0.-1.mca", "x": 28, "y": 32, "z": -80},
    {"dim": 1, "region_path": "world/DIM1/region/r.0.-1.mca", "x": 85, "y": 6, "z": -1},
    {"dim": 1, "region_path": "world/DIM1/region/r.0.0.mca", "x": 91, "y": 49, "z": 1},
    {"dim": 1, "region_path": "world/DIM1/region/r.0.0.mca", "x": 93, "y": 6, "z": 1},
]


def floor_div(a: int, b: int) -> int:
    return math.floor(a / b)


def pos_mod(a: int, b: int) -> int:
    return a % b


def region_rel_path(dim: int, region_x: int, region_z: int) -> str:
    name = f"r.{region_x}.{region_z}.mca"
    if dim == 0:
        return f"world/region/{name}"
    if dim == -1:
        return f"world/DIM-1/region/{name}"
    if dim == 1:
        return f"world/DIM1/region/{name}"
    raise ValueError(f"unsupported dim {dim}")


def chunk_coords_for_block(x: int, z: int) -> tuple[int, int]:
    return floor_div(x, 16), floor_div(z, 16)


def region_coords_for_chunk(chunk_x: int, chunk_z: int) -> tuple[int, int]:
    return floor_div(chunk_x, 32), floor_div(chunk_z, 32)


def nibble_get(buf: bytes | bytearray, idx: int) -> int:
    b = buf[idx >> 1]
    if idx & 1:
        return (b >> 4) & 0xF
    return b & 0xF


def nibble_set(buf: bytearray, idx: int, value: int) -> None:
    off = idx >> 1
    cur = buf[off]
    if idx & 1:
        buf[off] = (cur & 0x0F) | ((value & 0xF) << 4)
    else:
        buf[off] = (cur & 0xF0) | (value & 0xF)


class Tag:
    tag_id: int

    def clone(self):
        return copy.deepcopy(self)


@dataclass
class TagByte(Tag):
    value: int
    tag_id: int = TAG_BYTE


@dataclass
class TagShort(Tag):
    value: int
    tag_id: int = TAG_SHORT


@dataclass
class TagInt(Tag):
    value: int
    tag_id: int = TAG_INT


@dataclass
class TagLong(Tag):
    value: int
    tag_id: int = TAG_LONG


@dataclass
class TagFloat(Tag):
    value: float
    tag_id: int = TAG_FLOAT


@dataclass
class TagDouble(Tag):
    value: float
    tag_id: int = TAG_DOUBLE


@dataclass
class TagByteArray(Tag):
    value: bytes | bytearray
    tag_id: int = TAG_BYTE_ARRAY


@dataclass
class TagString(Tag):
    value: str
    tag_id: int = TAG_STRING


@dataclass
class TagList(Tag):
    elem_type: int
    value: list
    tag_id: int = TAG_LIST


@dataclass
class TagCompound(Tag):
    value: dict
    tag_id: int = TAG_COMPOUND


@dataclass
class TagIntArray(Tag):
    value: list[int]
    tag_id: int = TAG_INT_ARRAY


@dataclass
class TagLongArray(Tag):
    value: list[int]
    tag_id: int = TAG_LONG_ARRAY


class NBTReader:
    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0

    def read(self, n: int) -> bytes:
        if self.pos + n > len(self.data):
            raise EOFError("unexpected EOF")
        out = self.data[self.pos:self.pos + n]
        self.pos += n
        return out

    def u8(self) -> int:
        return self.read(1)[0]

    def i8(self) -> int:
        return struct.unpack(">b", self.read(1))[0]

    def u16(self) -> int:
        return struct.unpack(">H", self.read(2))[0]

    def i16(self) -> int:
        return struct.unpack(">h", self.read(2))[0]

    def i32(self) -> int:
        return struct.unpack(">i", self.read(4))[0]

    def i64(self) -> int:
        return struct.unpack(">q", self.read(8))[0]

    def f32(self) -> float:
        return struct.unpack(">f", self.read(4))[0]

    def f64(self) -> float:
        return struct.unpack(">d", self.read(8))[0]

    def string(self) -> str:
        n = self.u16()
        return self.read(n).decode("utf-8", "replace")

    def payload(self, tag_id: int) -> Tag:
        if tag_id == TAG_BYTE:
            return TagByte(self.i8())
        if tag_id == TAG_SHORT:
            return TagShort(self.i16())
        if tag_id == TAG_INT:
            return TagInt(self.i32())
        if tag_id == TAG_LONG:
            return TagLong(self.i64())
        if tag_id == TAG_FLOAT:
            return TagFloat(self.f32())
        if tag_id == TAG_DOUBLE:
            return TagDouble(self.f64())
        if tag_id == TAG_BYTE_ARRAY:
            n = self.i32()
            return TagByteArray(bytearray(self.read(n)))
        if tag_id == TAG_STRING:
            return TagString(self.string())
        if tag_id == TAG_LIST:
            elem_type = self.u8()
            n = self.i32()
            return TagList(elem_type, [self.payload(elem_type) for _ in range(n)])
        if tag_id == TAG_COMPOUND:
            return self.compound()
        if tag_id == TAG_INT_ARRAY:
            n = self.i32()
            return TagIntArray([self.i32() for _ in range(n)])
        if tag_id == TAG_LONG_ARRAY:
            n = self.i32()
            return TagLongArray([self.i64() for _ in range(n)])
        raise ValueError(f"unknown tag {tag_id}")

    def compound(self) -> TagCompound:
        out = {}
        while True:
            tag_id = self.u8()
            if tag_id == TAG_END:
                return TagCompound(out)
            name = self.string()
            out[name] = self.payload(tag_id)


class NBTWriter:
    def __init__(self):
        self.buf = bytearray()

    def write(self, b: bytes) -> None:
        self.buf.extend(b)

    def u8(self, n: int) -> None:
        self.buf.append(n & 0xFF)

    def i8(self, n: int) -> None:
        self.write(struct.pack(">b", n))

    def u16(self, n: int) -> None:
        self.write(struct.pack(">H", n))

    def i16(self, n: int) -> None:
        self.write(struct.pack(">h", n))

    def i32(self, n: int) -> None:
        self.write(struct.pack(">i", n))

    def i64(self, n: int) -> None:
        self.write(struct.pack(">q", n))

    def f32(self, v: float) -> None:
        self.write(struct.pack(">f", v))

    def f64(self, v: float) -> None:
        self.write(struct.pack(">d", v))

    def string(self, s: str) -> None:
        raw = s.encode("utf-8")
        self.u16(len(raw))
        self.write(raw)

    def payload(self, tag: Tag) -> None:
        if isinstance(tag, TagByte):
            self.i8(tag.value)
        elif isinstance(tag, TagShort):
            self.i16(tag.value)
        elif isinstance(tag, TagInt):
            self.i32(tag.value)
        elif isinstance(tag, TagLong):
            self.i64(tag.value)
        elif isinstance(tag, TagFloat):
            self.f32(tag.value)
        elif isinstance(tag, TagDouble):
            self.f64(tag.value)
        elif isinstance(tag, TagByteArray):
            raw = bytes(tag.value)
            self.i32(len(raw))
            self.write(raw)
        elif isinstance(tag, TagString):
            self.string(tag.value)
        elif isinstance(tag, TagList):
            self.u8(tag.elem_type)
            self.i32(len(tag.value))
            for item in tag.value:
                self.payload(item)
        elif isinstance(tag, TagCompound):
            for name, child in tag.value.items():
                self.u8(child.tag_id)
                self.string(name)
                self.payload(child)
            self.u8(TAG_END)
        elif isinstance(tag, TagIntArray):
            self.i32(len(tag.value))
            for n in tag.value:
                self.i32(n)
        elif isinstance(tag, TagLongArray):
            self.i32(len(tag.value))
            for n in tag.value:
                self.i64(n)
        else:
            raise TypeError(f"unsupported tag {type(tag)}")


def parse_nbt_document(raw: bytes) -> tuple[str, TagCompound]:
    reader = NBTReader(raw)
    tag_id = reader.u8()
    if tag_id != TAG_COMPOUND:
        raise ValueError(f"expected root compound, got {tag_id}")
    name = reader.string()
    return name, reader.compound()


def encode_nbt_document(name: str, root: TagCompound) -> bytes:
    writer = NBTWriter()
    writer.u8(TAG_COMPOUND)
    writer.string(name)
    writer.payload(root)
    return bytes(writer.buf)


def decompress_nbt(raw: bytes) -> tuple[bytes, str]:
    if len(raw) >= 2 and raw[:2] == b"\x1f\x8b":
        return gzip.decompress(raw), "gzip"
    try:
        return zlib.decompress(raw), "zlib"
    except zlib.error:
        return raw, "raw"


def tag_primitive(tag: Tag):
    if isinstance(tag, (TagByte, TagShort, TagInt, TagLong, TagFloat, TagDouble, TagString)):
        return tag.value
    raise TypeError(f"non-primitive tag: {type(tag)}")


def compound_get(compound: TagCompound, key: str, expected_type=None):
    tag = compound.value[key]
    if expected_type is not None and not isinstance(tag, expected_type):
        raise TypeError(f"{key} expected {expected_type}, got {type(tag)}")
    return tag


def compound_get_optional(compound: TagCompound, key: str):
    return compound.value.get(key)


def to_jsonable(tag: Tag):
    if isinstance(tag, (TagByte, TagShort, TagInt, TagLong, TagFloat, TagDouble, TagString)):
        return tag.value
    if isinstance(tag, TagByteArray):
        return {
            "type": "byte_array",
            "base64": base64.b64encode(bytes(tag.value)).decode("ascii"),
            "length": len(tag.value),
        }
    if isinstance(tag, TagIntArray):
        return {"type": "int_array", "value": tag.value}
    if isinstance(tag, TagLongArray):
        return {"type": "long_array", "value": tag.value}
    if isinstance(tag, TagList):
        return {"type": "list", "elem_type": tag.elem_type, "value": [to_jsonable(v) for v in tag.value]}
    if isinstance(tag, TagCompound):
        return {k: to_jsonable(v) for k, v in tag.value.items()}
    raise TypeError(f"unsupported tag {type(tag)}")


class DatHostClient:
    def __init__(self, env_path: Path):
        env = {}
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
        self.base = env["DATHOST_API_BASE"].rstrip("/")
        self.server = env["DATHOST_SERVER_ID"]
        auth = base64.b64encode(f"{env['DATHOST_API_EMAIL']}:{env['DATHOST_API_PASSWORD']}".encode()).decode()
        self.auth_header = {"Authorization": f"Basic {auth}"}

    def request(self, method: str, path: str, data=None, headers=None) -> bytes:
        req = urllib.request.Request(self.base + path, method=method)
        for k, v in self.auth_header.items():
            req.add_header(k, v)
        if headers:
            for k, v in headers.items():
                req.add_header(k, v)
        if data is not None:
            req.data = data
        with urllib.request.urlopen(req, timeout=180) as resp:
            return resp.read()

    def get_json(self, path: str):
        return json.loads(self.request("GET", path))

    def sync_files(self) -> None:
        self.request("POST", f"/game-servers/{self.server}/files/sync")

    def get_server(self):
        return self.get_json(f"/game-servers/{self.server}")

    def list_files(self, path: str):
        q = urllib.parse.quote(path, safe="")
        return self.get_json(f"/game-servers/{self.server}/files?path={q}")

    def download_file(self, path: str) -> bytes:
        enc = "/".join(urllib.parse.quote(p, safe="") for p in path.split("/"))
        return self.request("GET", f"/game-servers/{self.server}/files/{enc}")

    def upload_file(self, path: str, data: bytes) -> None:
        boundary = "----codex-boundary-" + hashlib.sha1(os.urandom(16)).hexdigest()
        body = bytearray()
        body.extend(f"--{boundary}\r\n".encode())
        body.extend(b'Content-Disposition: form-data; name="file"; filename="upload.bin"\r\n')
        body.extend(b"Content-Type: application/octet-stream\r\n\r\n")
        body.extend(data)
        body.extend(f"\r\n--{boundary}--\r\n".encode())
        enc = "/".join(urllib.parse.quote(p, safe="") for p in path.split("/"))
        self.request(
            "POST",
            f"/game-servers/{self.server}/files/{enc}",
            data=bytes(body),
            headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        )


class Chunk:
    def __init__(self, raw_nbt: bytes, compression: int):
        self.root_name, self.root = parse_nbt_document(raw_nbt)
        self.compression = compression
        self.dirty = False

    @property
    def level(self) -> TagCompound:
        return compound_get(self.root, "Level", TagCompound)

    def chunk_x(self) -> int:
        return tag_primitive(compound_get(self.level, "xPos", TagInt))

    def chunk_z(self) -> int:
        return tag_primitive(compound_get(self.level, "zPos", TagInt))

    def sections(self) -> list[TagCompound]:
        sections = compound_get(self.level, "Sections", TagList)
        return sections.value

    def tile_entities(self) -> list[TagCompound]:
        return compound_get(self.level, "TileEntities", TagList).value

    def mark_dirty(self) -> None:
        self.dirty = True

    def serialize(self) -> bytes:
        raw_nbt = encode_nbt_document(self.root_name, self.root)
        if self.compression == 1:
            buf = io.BytesIO()
            with gzip.GzipFile(fileobj=buf, mode="wb") as gz:
                gz.write(raw_nbt)
            payload = buf.getvalue()
        else:
            payload = zlib.compress(raw_nbt)
            self.compression = 2
        return struct.pack(">I", len(payload) + 1) + bytes([self.compression]) + payload

    def section_for_y(self, block_y: int, create: bool = False) -> TagCompound | None:
        want = block_y // 16
        sections_tag = compound_get(self.level, "Sections", TagList)
        prefer_16 = any("Blocks16" in section.value for section in sections_tag.value)
        for section in sections_tag.value:
            y_tag = compound_get(section, "Y", TagByte)
            if y_tag.value == want:
                return section
        if not create:
            return None
        if prefer_16:
            new_section = TagCompound({
                "Y": TagByte(want),
                "Blocks16": TagByteArray(bytearray(8192)),
                "Data16": TagByteArray(bytearray(8192)),
                "BlockLight": TagByteArray(bytearray(2048)),
                "SkyLight": TagByteArray(bytearray([0xFF] * 2048)),
            })
        else:
            new_section = TagCompound({
                "Y": TagByte(want),
                "Blocks": TagByteArray(bytearray(4096)),
                "Data": TagByteArray(bytearray(2048)),
                "BlockLight": TagByteArray(bytearray(2048)),
                "SkyLight": TagByteArray(bytearray([0xFF] * 2048)),
            })
        sections_tag.value.append(new_section)
        sections_tag.value.sort(key=lambda s: compound_get(s, "Y", TagByte).value)
        self.mark_dirty()
        return new_section

    def block_at(self, x: int, y: int, z: int) -> tuple[int, int]:
        section = self.section_for_y(y, create=False)
        if section is None:
            return 0, 0
        lx = pos_mod(x, 16)
        ly = pos_mod(y, 16)
        lz = pos_mod(z, 16)
        idx = (ly << 8) | (lz << 4) | lx
        blocks16_tag = compound_get_optional(section, "Blocks16")
        data16_tag = compound_get_optional(section, "Data16")
        if isinstance(blocks16_tag, TagByteArray):
            off = idx * 2
            block_id = struct.unpack(">H", bytes(blocks16_tag.value[off:off + 2]))[0]
            data = 0
            if isinstance(data16_tag, TagByteArray):
                data = struct.unpack(">H", bytes(data16_tag.value[off:off + 2]))[0]
            return block_id, data
        blocks = compound_get(section, "Blocks", TagByteArray).value
        block_id = blocks[idx]
        add_tag = compound_get_optional(section, "Add")
        if isinstance(add_tag, TagByteArray):
            block_id |= nibble_get(add_tag.value, idx) << 8
        data = nibble_get(compound_get(section, "Data", TagByteArray).value, idx)
        return block_id, data

    def set_block_at(self, x: int, y: int, z: int, block_id: int, meta: int) -> None:
        section = self.section_for_y(y, create=(block_id != 0 or meta != 0))
        if section is None:
            return
        lx = pos_mod(x, 16)
        ly = pos_mod(y, 16)
        lz = pos_mod(z, 16)
        idx = (ly << 8) | (lz << 4) | lx
        blocks16_tag = compound_get_optional(section, "Blocks16")
        data16_tag = compound_get_optional(section, "Data16")
        if isinstance(blocks16_tag, TagByteArray):
            off = idx * 2
            blocks16_tag.value[off:off + 2] = struct.pack(">H", block_id & 0xFFFF)
            if not isinstance(data16_tag, TagByteArray):
                data16_tag = TagByteArray(bytearray(8192))
                section.value["Data16"] = data16_tag
            data16_tag.value[off:off + 2] = struct.pack(">H", meta & 0xFFFF)
            self.mark_dirty()
            return
        blocks = compound_get(section, "Blocks", TagByteArray).value
        blocks[idx] = block_id & 0xFF
        if block_id > 0xFF:
            add_tag = compound_get_optional(section, "Add")
            if not isinstance(add_tag, TagByteArray):
                add_tag = TagByteArray(bytearray(2048))
                section.value["Add"] = add_tag
            nibble_set(add_tag.value, idx, (block_id >> 8) & 0xF)
        else:
            add_tag = compound_get_optional(section, "Add")
            if isinstance(add_tag, TagByteArray):
                nibble_set(add_tag.value, idx, 0)
        nibble_set(compound_get(section, "Data", TagByteArray).value, idx, meta & 0xF)
        self.mark_dirty()

    def remove_tile_entity_at(self, x: int, y: int, z: int) -> TagCompound:
        tes = compound_get(self.level, "TileEntities", TagList)
        for i, te in enumerate(tes.value):
            if (
                tag_primitive(compound_get(te, "x", TagInt)) == x and
                tag_primitive(compound_get(te, "y", TagInt)) == y and
                tag_primitive(compound_get(te, "z", TagInt)) == z
            ):
                self.mark_dirty()
                return tes.value.pop(i)
        raise KeyError(f"tile entity not found at {x},{y},{z}")

    def add_tile_entity(self, te: TagCompound) -> None:
        compound_get(self.level, "TileEntities", TagList).value.append(te)
        self.mark_dirty()


class RegionFile:
    def __init__(self, raw: bytes):
        if len(raw) < 8192:
            raise ValueError("invalid region file")
        self.timestamps = bytearray(raw[4096:8192])
        self.chunks = {}
        for i in range(1024):
            base = i * 4
            off = (raw[base] << 16) | (raw[base + 1] << 8) | raw[base + 2]
            sectors = raw[base + 3]
            if off == 0 or sectors == 0:
                continue
            start = off * 4096
            length = struct.unpack(">I", raw[start:start + 4])[0]
            compression = raw[start + 4]
            payload = raw[start + 5:start + 4 + length]
            if compression == 1:
                chunk_raw = gzip.decompress(payload)
            elif compression == 2:
                chunk_raw = zlib.decompress(payload)
            else:
                raise ValueError(f"unsupported compression {compression}")
            self.chunks[i] = Chunk(chunk_raw, compression)

    def get_chunk(self, chunk_x: int, chunk_z: int) -> Chunk:
        idx = (chunk_x % 32) + (chunk_z % 32) * 32
        return self.chunks[idx]

    def serialize(self) -> bytes:
        locations = bytearray(4096)
        timestamps = bytearray(self.timestamps)
        body = bytearray()
        sector = 2
        for idx in range(1024):
            chunk = self.chunks.get(idx)
            if chunk is None:
                continue
            payload = chunk.serialize()
            sectors_needed = (len(payload) + 4095) // 4096
            locations[idx * 4] = (sector >> 16) & 0xFF
            locations[idx * 4 + 1] = (sector >> 8) & 0xFF
            locations[idx * 4 + 2] = sector & 0xFF
            locations[idx * 4 + 3] = sectors_needed & 0xFF
            timestamps[idx * 4:idx * 4 + 4] = struct.pack(">I", int(time.time()))
            body.extend(payload)
            body.extend(b"\x00" * (sectors_needed * 4096 - len(payload)))
            sector += sectors_needed
        return bytes(locations + timestamps + body)


@dataclass
class GraveRecord:
    dim: int
    x: int
    y: int
    z: int
    region_path: str
    chunk_x: int
    chunk_z: int
    te: TagCompound
    block_id: int
    block_meta: int


def file_sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def load_region(client: DatHostClient, region_cache: dict, path: str) -> RegionFile:
    if path not in region_cache:
        region_cache[path] = {
            "original": client.download_file(path),
            "region": None,
        }
        region_cache[path]["region"] = RegionFile(region_cache[path]["original"])
    return region_cache[path]["region"]


def scan_graves(client: DatHostClient) -> list[GraveRecord]:
    out = []
    region_map = {}
    for rec in KNOWN_GRAVES:
        path = rec["region_path"]
        if path not in region_map:
            region_map[path] = RegionFile(client.download_file(path))
    for rec in KNOWN_GRAVES:
        region = region_map[rec["region_path"]]
        chunk_x, chunk_z = chunk_coords_for_block(rec["x"], rec["z"])
        chunk = region.get_chunk(chunk_x, chunk_z)
        match = None
        for te in chunk.tile_entities():
            te_id = tag_primitive(compound_get(te, "id", TagString))
            if te_id != "openblocks_grave":
                continue
            x = tag_primitive(compound_get(te, "x", TagInt))
            y = tag_primitive(compound_get(te, "y", TagInt))
            z = tag_primitive(compound_get(te, "z", TagInt))
            if (x, y, z) == (rec["x"], rec["y"], rec["z"]):
                match = te
                break
        if match is None:
            raise RuntimeError(f"expected grave missing at {rec}")
        block_id, block_meta = chunk.block_at(rec["x"], rec["y"], rec["z"])
        out.append(GraveRecord(rec["dim"], rec["x"], rec["y"], rec["z"], rec["region_path"], chunk_x, chunk_z, match.clone(), block_id, block_meta))
    out.sort(key=lambda g: (g.dim, g.x, g.y, g.z))
    print(f"loaded {len(out)} known graves from {len(region_map)} regions", flush=True)
    return out


def get_spawn(client: DatHostClient) -> tuple[int, int, int]:
    raw = client.download_file("world/level.dat")
    inflated, _ = decompress_nbt(raw)
    _, root = parse_nbt_document(inflated)
    data = compound_get(root, "Data", TagCompound)
    return (
        tag_primitive(compound_get(data, "SpawnX", TagInt)),
        tag_primitive(compound_get(data, "SpawnY", TagInt)),
        tag_primitive(compound_get(data, "SpawnZ", TagInt)),
    )


def find_surface_y(chunk: Chunk, x: int, z: int) -> int | None:
    for y in range(255, -1, -1):
        block_id, _ = chunk.block_at(x, y, z)
        if block_id not in PASSABLE_BLOCK_IDS:
            return y
    return None


def candidate_positions(spawn_x: int, spawn_z: int):
    step = 3
    for radius in range(2, 20):
        xs = range(spawn_x - radius * step, spawn_x + radius * step + 1, step)
        zs = range(spawn_z - radius * step, spawn_z + radius * step + 1, step)
        for z in zs:
            for x in xs:
                yield x, z


def plan_destinations(client: DatHostClient, region_cache: dict, spawn_x: int, spawn_z: int, count: int):
    planned = []
    used = set()
    for x, z in candidate_positions(spawn_x, spawn_z):
        if len(planned) >= count:
            break
        chunk_x, chunk_z = chunk_coords_for_block(x, z)
        region_x, region_z = region_coords_for_chunk(chunk_x, chunk_z)
        path = region_rel_path(0, region_x, region_z)
        region = load_region(client, region_cache, path)
        chunk = region.get_chunk(chunk_x, chunk_z)
        ground_y = find_surface_y(chunk, x, z)
        if ground_y is None:
            continue
        if chunk.block_at(x, ground_y, z)[0] in UNSAFE_SUPPORT_BLOCK_IDS:
            continue
        target_y = ground_y + 1
        if target_y >= 255:
            continue
        block_here, _ = chunk.block_at(x, target_y, z)
        block_above, _ = chunk.block_at(x, target_y + 1, z)
        if block_here not in PASSABLE_BLOCK_IDS or block_above not in PASSABLE_BLOCK_IDS:
            continue
        collision = False
        for te in chunk.tile_entities():
            tx = tag_primitive(compound_get(te, "x", TagInt))
            ty = tag_primitive(compound_get(te, "y", TagInt))
            tz = tag_primitive(compound_get(te, "z", TagInt))
            if tx == x and ty == target_y and tz == z:
                collision = True
                break
        if collision or (x, target_y, z) in used:
            continue
        used.add((x, target_y, z))
        planned.append({
            "x": x,
            "y": target_y,
            "z": z,
            "region_path": path,
            "chunk_x": chunk_x,
            "chunk_z": chunk_z,
            "ground_y": ground_y,
        })
    if len(planned) < count:
        raise RuntimeError(f"only found {len(planned)} destination pads for {count} graves")
    return planned


def backup_bundle(client: DatHostClient, base_dir: Path, region_cache: dict, graves: list[GraveRecord], plan: list[dict]) -> None:
    base_dir.mkdir(parents=True, exist_ok=True)
    grave_json = []
    affected = set(g.region_path for g in graves)
    affected.update(p["region_path"] for p in plan)
    raw_dir = base_dir / "original_regions"
    raw_dir.mkdir(parents=True, exist_ok=True)
    for path in sorted(affected):
        if path not in region_cache:
            load_region(client, region_cache, path)
        rec = region_cache[path]
        target = raw_dir / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(rec["original"])
    for grave, dest in zip(graves, plan):
        grave_json.append({
            "from": {"dim": grave.dim, "x": grave.x, "y": grave.y, "z": grave.z, "region_path": grave.region_path},
            "to": dest,
            "block_id": grave.block_id,
            "block_meta": grave.block_meta,
            "perishedUsername": tag_primitive(compound_get(grave.te, "perishedUsername", TagString)),
            "message": tag_primitive(compound_get(grave.te, "Message", TagString)),
            "items_count": len(compound_get(grave.te, "Items", TagList).value),
            "tile_entity": to_jsonable(grave.te),
        })
    (base_dir / "grave_backup.json").write_text(json.dumps(grave_json, indent=2))
    manifest = {
        "created_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "grave_count": len(graves),
        "affected_region_count": len(affected),
        "moves": grave_json,
    }
    (base_dir / "plan.json").write_text(json.dumps(manifest, indent=2))


def apply_moves(client: DatHostClient, region_cache: dict, graves: list[GraveRecord], plan: list[dict], backup_dir: Path) -> list[dict]:
    for grave, dest in zip(graves, plan):
        src_region = load_region(client, region_cache, grave.region_path)
        src_chunk = src_region.get_chunk(grave.chunk_x, grave.chunk_z)
        src_chunk.set_block_at(grave.x, grave.y, grave.z, 0, 0)
        moved_te = src_chunk.remove_tile_entity_at(grave.x, grave.y, grave.z)

        dest_region = load_region(client, region_cache, dest["region_path"])
        dest_chunk = dest_region.get_chunk(dest["chunk_x"], dest["chunk_z"])
        moved_te.value["x"] = TagInt(dest["x"])
        moved_te.value["y"] = TagInt(dest["y"])
        moved_te.value["z"] = TagInt(dest["z"])
        dest_chunk.set_block_at(dest["x"], dest["y"], dest["z"], grave.block_id, grave.block_meta)
        dest_chunk.add_tile_entity(moved_te)

    modified_dir = backup_dir / "modified_regions"
    modified_dir.mkdir(parents=True, exist_ok=True)
    uploads = []
    for path, rec in sorted(region_cache.items()):
        region: RegionFile = rec["region"]
        if not any(chunk.dirty for chunk in region.chunks.values()):
            continue
        data = region.serialize()
        target = modified_dir / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        uploads.append({
            "path": path,
            "sha256_before": file_sha256(rec["original"]),
            "sha256_after": file_sha256(data),
            "size_before": len(rec["original"]),
            "size_after": len(data),
        })
        client.upload_file(path, data)
    (backup_dir / "uploaded_files.json").write_text(json.dumps(uploads, indent=2))
    return uploads


def verify(client: DatHostClient, graves: list[GraveRecord], plan: list[dict]) -> dict:
    affected_paths = sorted(set(g.region_path for g in graves) | set(p["region_path"] for p in plan))
    found = {}
    for path in affected_paths:
        if "/DIM-1/" in path:
            dim = -1
        elif "/DIM1/" in path:
            dim = 1
        else:
            dim = 0
        region = RegionFile(client.download_file(path))
        for chunk in region.chunks.values():
            for te in chunk.tile_entities():
                te_id = tag_primitive(compound_get(te, "id", TagString))
                if te_id != "openblocks_grave":
                    continue
                x = tag_primitive(compound_get(te, "x", TagInt))
                y = tag_primitive(compound_get(te, "y", TagInt))
                z = tag_primitive(compound_get(te, "z", TagInt))
                found[(dim, x, y, z)] = te
    missing_sources = []
    missing_destinations = []
    for grave in graves:
        if (grave.dim, grave.x, grave.y, grave.z) in found:
            missing_sources.append({"dim": grave.dim, "x": grave.x, "y": grave.y, "z": grave.z})
    for grave, dest in zip(graves, plan):
        hit = found.get((0, dest["x"], dest["y"], dest["z"]))
        if hit is None:
            missing_destinations.append(dest)
            continue
        if len(compound_get(hit, "Items", TagList).value) != len(compound_get(grave.te, "Items", TagList).value):
            missing_destinations.append({"dest": dest, "reason": "item_count_mismatch"})
    return {
        "post_grave_count_in_affected_regions": len(found),
        "missing_sources": missing_sources,
        "missing_destinations": missing_destinations,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", default="deploy/env/dathost-bridge.env")
    parser.add_argument("--backup-root", default="workspace/state")
    args = parser.parse_args()

    client = DatHostClient(Path(args.env_file))
    server = client.get_server()
    if server.get("on"):
        raise SystemExit("server is still on; aborting")

    client.sync_files()
    backup_dir = Path(args.backup_root) / f"grave_move_{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}"
    spawn_x, spawn_y, spawn_z = get_spawn(client)
    graves = scan_graves(client)
    if not graves:
        raise SystemExit("no openblocks_grave records found")

    region_cache = {}
    plan = plan_destinations(client, region_cache, spawn_x, spawn_z, len(graves))
    backup_bundle(client, backup_dir, region_cache, graves, plan)
    uploads = apply_moves(client, region_cache, graves, plan, backup_dir)
    client.sync_files()
    verification = verify(client, graves, plan)
    (backup_dir / "verification.json").write_text(json.dumps(verification, indent=2))

    summary = {
        "spawn": {"x": spawn_x, "y": spawn_y, "z": spawn_z},
        "grave_count": len(graves),
        "uploaded_region_files": uploads,
        "backup_dir": str(backup_dir),
        "verification": verification,
    }
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
