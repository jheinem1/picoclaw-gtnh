#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


DISCORD_API_BASE = "https://discord.com/api/v10"
DEFAULT_DATA_FILE = "questbooks/atmons-0.14.1-beta.json"
DEFAULT_STATE_FILE = "state/atmons-questbook-state.json"
DEFAULT_SYNC_STATE_FILE = "state/atmons-questbook-sync.json"
DEFAULT_VERSION = "0.14.1-beta"
DEFAULT_RELEASE_DATE = "2026-03-31"
DEFAULT_SOURCE_COMMIT = "e9eff57bd65190331ccdf20bf67c5f59dff1a206"
DEFAULT_SOURCE_REPO = "https://github.com/AllTheMods/All-the-Mons.git"
DEFAULT_SOURCE_NOTE = (
    "Pinned to the last upstream questbook commit on 2026-03-31 because the upstream repo "
    "does not publish a 0.14.1-beta git tag."
)

FORMAT_CODE_RE = re.compile(r"(?:§|&)[0-9A-FK-ORa-fk-or]")
HEX_FORMAT_CODE_RE = re.compile(r"&#[0-9A-Fa-f]{6}")
WHITESPACE_RE = re.compile(r"[ \t]+")
NUMBER_RE = re.compile(r"^[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:[eE][+-]?\d+)?[bBsSlLfFdD]?$")
QUEST_ID_RE = re.compile(r"^[0-9A-F]{16}$")


class ParseError(RuntimeError):
    pass


@dataclass
class Token:
    kind: str
    value: str
    pos: int


class Lexer:
    def __init__(self, text: str) -> None:
        self.text = text
        self.pos = 0

    def _skip_ws(self) -> None:
        n = len(self.text)
        while self.pos < n and self.text[self.pos].isspace():
            self.pos += 1

    def next(self) -> Token:
        self._skip_ws()
        if self.pos >= len(self.text):
            return Token("EOF", "", self.pos)
        ch = self.text[self.pos]
        if ch in "{}[]:,":
            self.pos += 1
            return Token(ch, ch, self.pos - 1)
        if ch == '"':
            return self._read_string()
        start = self.pos
        while self.pos < len(self.text) and self.text[self.pos] not in '{}[]:,"' and not self.text[self.pos].isspace():
            self.pos += 1
        return Token("ATOM", self.text[start:self.pos], start)

    def _read_string(self) -> Token:
        start = self.pos
        self.pos += 1
        out: list[str] = []
        while self.pos < len(self.text):
            ch = self.text[self.pos]
            self.pos += 1
            if ch == '"':
                return Token("STRING", "".join(out), start)
            if ch == "\\":
                if self.pos >= len(self.text):
                    raise ParseError(f"unterminated escape at {start}")
                esc = self.text[self.pos]
                self.pos += 1
                mapping = {
                    '"': '"',
                    "\\": "\\",
                    "/": "/",
                    "b": "\b",
                    "f": "\f",
                    "n": "\n",
                    "r": "\r",
                    "t": "\t",
                }
                if esc == "u":
                    chunk = self.text[self.pos:self.pos + 4]
                    if len(chunk) != 4 or not re.fullmatch(r"[0-9A-Fa-f]{4}", chunk):
                        raise ParseError(f"invalid unicode escape at {self.pos}")
                    out.append(chr(int(chunk, 16)))
                    self.pos += 4
                    continue
                out.append(mapping.get(esc, esc))
                continue
            out.append(ch)
        raise ParseError(f"unterminated string at {start}")


class Parser:
    def __init__(self, text: str) -> None:
        self.lexer = Lexer(text)
        self.peek = self.lexer.next()

    def consume(self, kind: str | None = None) -> Token:
        tok = self.peek
        if kind is not None and tok.kind != kind:
            raise ParseError(f"expected {kind}, got {tok.kind} at {tok.pos}")
        self.peek = self.lexer.next()
        return tok

    def parse(self) -> Any:
        value = self.parse_value()
        if self.peek.kind != "EOF":
            raise ParseError(f"unexpected token {self.peek.kind} at {self.peek.pos}")
        return value

    def parse_value(self) -> Any:
        tok = self.peek
        if tok.kind == "{":
            return self.parse_object()
        if tok.kind == "[":
            return self.parse_array()
        if tok.kind == "STRING":
            return self.consume("STRING").value
        if tok.kind == "ATOM":
            return self._parse_atom(self.consume("ATOM").value)
        raise ParseError(f"unexpected token {tok.kind} at {tok.pos}")

    def parse_object(self) -> dict[str, Any]:
        self.consume("{")
        out: dict[str, Any] = {}
        while self.peek.kind != "}":
            if self.peek.kind not in {"ATOM", "STRING"}:
                raise ParseError(f"expected object key, got {self.peek.kind} at {self.peek.pos}")
            key = self.consume(self.peek.kind).value
            self.consume(":")
            out[key] = self.parse_value()
            if self.peek.kind == ",":
                self.consume(",")
        self.consume("}")
        return out

    def parse_array(self) -> list[Any]:
        self.consume("[")
        out: list[Any] = []
        while self.peek.kind != "]":
            out.append(self.parse_value())
            if self.peek.kind == ",":
                self.consume(",")
        self.consume("]")
        return out

    @staticmethod
    def _parse_atom(raw: str) -> Any:
        lower = raw.lower()
        if lower == "true":
            return True
        if lower == "false":
            return False
        if NUMBER_RE.match(raw):
            suffix = raw[-1].lower() if raw[-1].isalpha() else ""
            if suffix:
                raw = raw[:-1]
            if any(ch in raw for ch in ".eE"):
                try:
                    return float(raw)
                except ValueError:
                    return raw
            try:
                return int(raw, 10)
            except ValueError:
                return raw
        return raw


def parse_snbt(text: str) -> Any:
    return Parser(text).parse()


def load_snbt(path: Path) -> Any:
    return parse_snbt(path.read_text(encoding="utf-8"))


def dump_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def now_utc() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def clean_text(value: str) -> str:
    text = value.replace("\\&", "&").replace("\\'", "'")
    text = HEX_FORMAT_CODE_RE.sub("", text)
    text = FORMAT_CODE_RE.sub("", text)
    text = text.replace("\r", "")
    lines = [WHITESPACE_RE.sub(" ", line).strip() for line in text.split("\n")]
    return "\n".join(line for line in lines if line).strip()


def first_text(value: Any) -> str:
    if isinstance(value, str):
        return clean_text(value)
    if isinstance(value, list):
        return clean_text("\n".join(str(v) for v in value))
    return clean_text(str(value))


def truncate(text: str, limit: int) -> str:
    text = text.strip()
    if len(text) <= limit:
        return text
    if limit <= 3:
        return text[:limit]
    return text[: limit - 3].rstrip() + "..."


def first_sentence(text: str) -> str:
    text = clean_text(text)
    if not text:
        return ""
    line = text.split("\n", 1)[0].strip()
    for sep in [". ", "! ", "? "]:
        if sep in line:
            return line.split(sep, 1)[0].strip() + line[line.find(sep) : line.find(sep) + 1]
    return line


def display_quest_title(quest: dict[str, Any]) -> str:
    title = clean_text(quest.get("title", ""))
    quest_id = str(quest.get("id", "")).strip()
    if title and title != quest_id and not QUEST_ID_RE.fullmatch(title):
        return title

    task_titles = []
    for raw in quest.get("task_titles", []):
        cleaned = clean_text(str(raw))
        if cleaned and cleaned.lower() != "allrightsreserved":
            task_titles.append(cleaned)
    if task_titles:
        unique = list(dict.fromkeys(task_titles))
        if len(unique) == 1:
            return unique[0]
        return truncate(" / ".join(unique), 120)

    subtitle = clean_text(quest.get("subtitle", ""))
    desc = first_sentence(quest.get("description", ""))
    if subtitle and desc and subtitle.lower() not in desc.lower():
        return truncate(f"{subtitle} | {desc}", 120)
    if subtitle:
        return subtitle
    if desc:
        return truncate(desc, 120)
    return title or quest_id


def load_json_file(path: Path, default: Any) -> Any:
    if not path.exists():
        return default
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def save_json_file(path: Path, value: Any) -> None:
    dump_json(path, value)


def chapter_sort_key(group_order: dict[str, int], chapter: dict[str, Any]) -> tuple[Any, ...]:
    group = chapter.get("group_id") or ""
    return (
        group_order.get(group, 10_000),
        chapter.get("order_index", 0),
        clean_text(chapter.get("group_title", "")),
        clean_text(chapter.get("title", "")),
        chapter.get("filename", ""),
    )


def quest_sort_key(quest: dict[str, Any]) -> tuple[Any, ...]:
    return (
        float(quest.get("y", 0.0)),
        float(quest.get("x", 0.0)),
        quest.get("title", ""),
        quest.get("id", ""),
    )


def extract_questbook(args: argparse.Namespace) -> int:
    source_dir = Path(args.source_dir).resolve()
    quests_dir = source_dir / "config" / "ftbquests" / "quests"
    chapters_dir = quests_dir / "chapters"
    lang_dir = quests_dir / "lang" / "en_us"
    lang_chapters_dir = lang_dir / "chapters"
    if not chapters_dir.is_dir():
        raise SystemExit(f"missing chapters dir: {chapters_dir}")

    chapter_lang = load_snbt(lang_dir / "chapter.snbt")
    group_lang = load_snbt(lang_dir / "chapter_group.snbt")
    group_index_doc = load_snbt(quests_dir / "chapter_groups.snbt")
    data_doc = load_snbt(quests_dir / "data.snbt")
    group_order = {
        row.get("id", ""): idx
        for idx, row in enumerate(group_index_doc.get("chapter_groups", []))
        if isinstance(row, dict) and row.get("id")
    }

    chapters: list[dict[str, Any]] = []
    all_quests: dict[str, dict[str, Any]] = {}
    all_visible = 0
    all_hidden = 0

    for chapter_path in sorted(chapters_dir.glob("*.snbt")):
        chapter_doc = load_snbt(chapter_path)
        lang_doc = {}
        lang_path = lang_chapters_dir / chapter_path.name
        if lang_path.exists():
            lang_doc = load_snbt(lang_path)

        chapter_id = str(chapter_doc.get("id", "")).strip()
        chapter_title = first_text(chapter_lang.get(f"chapter.{chapter_id}.title", chapter_doc.get("filename", chapter_path.stem)))
        chapter_subtitle = first_text(chapter_lang.get(f"chapter.{chapter_id}.chapter_subtitle", ""))
        group_id = str(chapter_doc.get("group", "")).strip()
        group_title = first_text(group_lang.get(f"chapter_group.{group_id}.title", "")) if group_id else ""
        quests: list[dict[str, Any]] = []
        for row in chapter_doc.get("quests", []):
            if not isinstance(row, dict):
                continue
            quest_id = str(row.get("id", "")).strip()
            quest_title = first_text(lang_doc.get(f"quest.{quest_id}.title", ""))
            quest_desc = first_text(lang_doc.get(f"quest.{quest_id}.quest_desc", ""))
            quest_subtitle = first_text(lang_doc.get(f"quest.{quest_id}.quest_subtitle", ""))
            tasks = row.get("tasks", [])
            task_titles = []
            task_types = []
            for task in tasks if isinstance(tasks, list) else []:
                if not isinstance(task, dict):
                    continue
                task_id = str(task.get("id", "")).strip()
                task_title = first_text(lang_doc.get(f"task.{task_id}.title", ""))
                if task_title:
                    task_titles.append(task_title)
                task_type = str(task.get("type", "")).strip()
                if task_type:
                    task_types.append(task_type)
            if not quest_title:
                if len(task_titles) == 1:
                    quest_title = task_titles[0]
                else:
                    quest_title = quest_id
            hidden = bool(row.get("invisible", False))
            if hidden:
                all_hidden += 1
            else:
                all_visible += 1
            quest = {
                "id": quest_id,
                "title": quest_title or quest_id,
                "subtitle": quest_subtitle,
                "description": quest_desc,
                "task_titles": task_titles,
                "task_types": task_types,
                "dependencies": [str(dep) for dep in row.get("dependencies", []) if str(dep).strip()],
                "x": float(row.get("x", 0.0)),
                "y": float(row.get("y", 0.0)),
                "shape": str(row.get("shape", chapter_doc.get("default_quest_shape", "")) or ""),
                "size": float(row.get("size", 1.0)),
                "hidden": hidden,
                "optional": bool(row.get("optional", False)),
                "chapter_id": chapter_id,
                "chapter_title": chapter_title,
            }
            quests.append(quest)
            all_quests[quest_id] = quest

        quests.sort(key=quest_sort_key)
        chapters.append(
            {
                "id": chapter_id,
                "filename": str(chapter_doc.get("filename", chapter_path.stem)),
                "title": chapter_title or chapter_path.stem,
                "subtitle": chapter_subtitle,
                "group_id": group_id,
                "group_title": group_title,
                "order_index": int(chapter_doc.get("order_index", 0)),
                "icon": chapter_doc.get("icon", {}),
                "quests": quests,
                "default_hide_dependency_lines": bool(chapter_doc.get("default_hide_dependency_lines", False)),
                "progression_mode": str(chapter_doc.get("progression_mode", data_doc.get("progression_mode", ""))),
            }
        )

    dependents: dict[str, list[str]] = {}
    for quest in all_quests.values():
        for dep in quest.get("dependencies", []):
            dependents.setdefault(dep, []).append(quest["id"])
    for quest in all_quests.values():
        unlocked = sorted(dependents.get(quest["id"], []))
        quest["dependents"] = unlocked
        quest["is_milestone"] = bool(unlocked) or quest.get("size", 1.0) >= 1.5 or quest.get("shape") in {
            "pentagon",
            "hexagon",
            "octagon",
            "diamond",
        }

    chapters.sort(key=lambda chapter: chapter_sort_key(group_order, chapter))

    doc = {
        "version": 1,
        "pack": {
            "name": args.pack_name,
            "display_name": args.display_name,
            "version_label": args.version_label,
            "release_date": args.release_date,
        },
        "source": {
            "repo": args.source_repo,
            "commit": args.source_commit,
            "note": args.source_note,
            "generated_at": now_utc(),
        },
        "stats": {
            "chapter_count": len(chapters),
            "quest_count": len(all_quests),
            "visible_quest_count": all_visible,
            "hidden_quest_count": all_hidden,
        },
        "chapters": chapters,
    }
    dump_json(Path(args.output_file), doc)
    print(
        f"wrote {args.output_file} ({len(chapters)} chapters, {len(all_quests)} quests, "
        f"{all_visible} visible, {all_hidden} hidden)"
    )
    return 0


def load_tracker(data_file: Path, state_file: Path) -> tuple[dict[str, Any], dict[str, Any], dict[str, dict[str, Any]]]:
    data = load_json_file(data_file, None)
    if not data:
        raise SystemExit(f"missing questbook data file: {data_file}")
    state = load_json_file(state_file, {"version": 1, "completed": {}})
    state.setdefault("version", 1)
    state.setdefault("completed", {})
    quest_index: dict[str, dict[str, Any]] = {}
    for chapter in data.get("chapters", []):
        for quest in chapter.get("quests", []):
            quest_index[quest["id"]] = quest
    return data, state, quest_index


def resolve_query(query: str, quest_index: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    needle = query.strip()
    if not needle:
        return []
    if needle in quest_index:
        return [quest_index[needle]]
    lower = needle.lower()
    matches = []
    for quest in quest_index.values():
        hay = " ".join(
            [
                quest.get("id", ""),
                quest.get("title", ""),
                quest.get("subtitle", ""),
                quest.get("chapter_title", ""),
            ]
        ).lower()
        if lower in hay:
            matches.append(quest)
    matches.sort(key=lambda quest: (quest.get("chapter_title", ""), quest.get("title", ""), quest.get("id", "")))
    return matches


def cmd_status(args: argparse.Namespace) -> int:
    data, state, quest_index = load_tracker(Path(args.data_file), Path(args.state_file))
    completed = {
        quest_id: meta
        for quest_id, meta in state.get("completed", {}).items()
        if quest_id in quest_index and not quest_index[quest_id].get("hidden")
    }
    print(f"{data['pack']['display_name']} {data['pack']['version_label']}")
    print(f"chapters: {data['stats']['chapter_count']}")
    print(f"quests: {data['stats']['visible_quest_count']} visible / {data['stats']['quest_count']} total")
    print(f"completed: {len(completed)}")
    return 0


def format_match(quest: dict[str, Any]) -> str:
    marker = "milestone" if quest.get("is_milestone") else "quest"
    hidden = " hidden" if quest.get("hidden") else ""
    return f"{quest['id']} | {quest['chapter_title']} | {display_quest_title(quest)} [{marker}{hidden}]"


def cmd_find(args: argparse.Namespace) -> int:
    _, _, quest_index = load_tracker(Path(args.data_file), Path(args.state_file))
    matches = resolve_query(args.query, quest_index)
    if not matches:
        print(f"no matches for {args.query!r}")
        return 1
    for quest in matches[: args.limit]:
        print(format_match(quest))
    if len(matches) > args.limit:
        print(f"... {len(matches) - args.limit} more")
    return 0


def _choose_single(query: str, quest_index: dict[str, dict[str, Any]]) -> dict[str, Any]:
    matches = resolve_query(query, quest_index)
    if not matches:
        raise SystemExit(f"no quest matches {query!r}")
    if len(matches) > 1:
        sample = "\n".join(format_match(quest) for quest in matches[:8])
        more = "" if len(matches) <= 8 else f"\n... {len(matches) - 8} more"
        raise SystemExit(f"query {query!r} is ambiguous:\n{sample}{more}")
    return matches[0]


def cmd_show(args: argparse.Namespace) -> int:
    _, state, quest_index = load_tracker(Path(args.data_file), Path(args.state_file))
    quest = _choose_single(args.query, quest_index)
    completed = state.get("completed", {}).get(quest["id"])
    print(f"id: {quest['id']}")
    print(f"chapter: {quest['chapter_title']}")
    print(f"title: {display_quest_title(quest)}")
    if quest.get("subtitle"):
        print(f"subtitle: {quest['subtitle']}")
    print(f"status: {'completed' if completed else 'open'}")
    if completed:
        print(f"completed_at: {completed.get('completed_at', '')}")
        print(f"completed_by: {completed.get('completed_by', '')}")
    if quest.get("description"):
        print("description:")
        print(quest["description"])
    if quest.get("task_titles"):
        print("tasks:")
        for task_title in quest["task_titles"]:
            print(f"- {task_title}")
    return 0


def cmd_complete(args: argparse.Namespace) -> int:
    data_file = Path(args.data_file)
    state_file = Path(args.state_file)
    _, state, quest_index = load_tracker(data_file, state_file)
    quest = _choose_single(args.query, quest_index)
    state["completed"][quest["id"]] = {
        "completed_at": now_utc(),
        "completed_by": args.by,
        "note": args.note,
        "source": "manual",
    }
    save_json_file(state_file, state)
    print(f"completed {quest['id']} | {quest['chapter_title']} | {quest['title']}")
    return 0


def cmd_reopen(args: argparse.Namespace) -> int:
    data_file = Path(args.data_file)
    state_file = Path(args.state_file)
    _, state, quest_index = load_tracker(data_file, state_file)
    quest = _choose_single(args.query, quest_index)
    if state.get("completed", {}).pop(quest["id"], None) is None:
        print(f"quest already open: {quest['id']} | {quest['title']}")
        return 0
    save_json_file(state_file, state)
    print(f"reopened {quest['id']} | {quest['chapter_title']} | {quest['title']}")
    return 0


def build_chapter_payload(chapter: dict[str, Any], state: dict[str, Any], version_label: str) -> dict[str, Any]:
    completed = state.get("completed", {})
    visible_quests = [quest for quest in chapter.get("quests", []) if not quest.get("hidden")]
    milestone_total = sum(1 for quest in visible_quests if quest.get("is_milestone"))
    milestone_done = sum(1 for quest in visible_quests if quest.get("is_milestone") and quest["id"] in completed)
    quest_done = sum(1 for quest in visible_quests if quest["id"] in completed)

    line_variants: list[str] = []
    for index, quest in enumerate(visible_quests, start=1):
        if quest["id"] in completed:
            prefix = "✅"
        elif quest.get("is_milestone"):
            prefix = "⭐"
        else:
            prefix = "⬜"
        title = display_quest_title(quest)
        subtitle = clean_text(quest.get("subtitle", ""))
        if subtitle and subtitle not in title:
            title = f"{title} | {subtitle}"
        line_variants.append(f"{prefix} {index}. {title}".strip())

    use_compact = False
    total_chars = sum(len(line) + 1 for line in line_variants)
    if total_chars > 5200:
        use_compact = True
        line_variants = []
        for quest in visible_quests:
            prefix = "✅" if quest["id"] in completed else ("⭐" if quest.get("is_milestone") else "⬜")
            line_variants.append(f"{prefix} {display_quest_title(quest)}")

    chunks: list[str] = []
    current: list[str] = []
    current_len = 0
    max_chunk = 950
    for line in line_variants:
        addition = len(line) + (1 if current else 0)
        if current and current_len + addition > max_chunk:
            chunks.append("\n".join(current))
            current = [line]
            current_len = len(line)
            continue
        current.append(line)
        current_len += addition
    if current:
        chunks.append("\n".join(current))

    footer_bits = [f"Version {version_label}", f"{quest_done}/{len(visible_quests)} complete"]
    if milestone_total:
        footer_bits.append(f"{milestone_done}/{milestone_total} milestones")
    footer_bits.append("order: chapter graph y/x")

    fields = [
        {
            "name": "Progress",
            "value": f"Quests: {quest_done}/{len(visible_quests)}\nMilestones: {milestone_done}/{milestone_total or 0}",
            "inline": True,
        }
    ]
    if chapter.get("group_title"):
        fields.append({"name": "Group", "value": chapter["group_title"], "inline": True})
    fields.append({"name": "Quest Count", "value": str(len(visible_quests)), "inline": True})
    for idx, chunk in enumerate(chunks, start=1):
        fields.append(
            {
                "name": "Quests" if idx == 1 else f"Quests {idx}",
                "value": chunk or "(none)",
                "inline": False,
            }
        )
    embed = {
        "title": chapter.get("title", chapter.get("filename", "Chapter")),
        "description": truncate(chapter.get("subtitle", ""), 4096),
        "color": 0x3B82F6,
        "fields": fields[:25],
        "footer": {"text": " | ".join(bit for bit in footer_bits if bit)},
    }
    return {"embeds": [embed]}


def cmd_render_json(args: argparse.Namespace) -> int:
    data, state, _ = load_tracker(Path(args.data_file), Path(args.state_file))
    payloads = []
    for chapter in data.get("chapters", []):
        payload = build_chapter_payload(chapter, state, data["pack"]["version_label"])
        payloads.append(
            {
                "chapter_id": chapter["id"],
                "chapter_title": chapter["title"],
                "payload": payload,
                "hash": hashlib.sha1(json.dumps(payload, sort_keys=True).encode("utf-8")).hexdigest(),
            }
        )
    print(json.dumps({"messages": payloads}, ensure_ascii=False))
    return 0


def discord_request(method: str, path: str, token: str, body: dict[str, Any] | None = None) -> tuple[bytes, int]:
    url = DISCORD_API_BASE + path
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", f"Bot {token}")
    req.add_header("User-Agent", "greggpt-questbook-sync/1.0")
    if data is not None:
        req.add_header("Content-Type", "application/json")

    for attempt in range(4):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return resp.read(), resp.getcode()
        except urllib.error.HTTPError as err:
            payload = err.read()
            if err.code == 429 and attempt < 3:
                retry_after = err.headers.get("Retry-After", "1")
                try:
                    sleep_for = float(retry_after)
                except ValueError:
                    sleep_for = 1.0
                time.sleep(max(0.5, sleep_for))
                continue
            if err.code >= 500 and attempt < 3:
                time.sleep(1.5 * (attempt + 1))
                continue
            raise SystemExit(f"discord api error {method} {path}: HTTP {err.code}: {payload.decode('utf-8', 'replace').strip()}")
        except urllib.error.URLError as err:
            if attempt < 3:
                time.sleep(1.5 * (attempt + 1))
                continue
            raise SystemExit(f"discord api error {method} {path}: {err}") from err
    raise SystemExit(f"discord api error {method} {path}: exhausted retries")


def cmd_sync_channel(args: argparse.Namespace) -> int:
    data, state, _ = load_tracker(Path(args.data_file), Path(args.state_file))
    token = (args.bot_token or os.environ.get("DISCORD_BOT_TOKEN") or os.environ.get("GREGGPT_DISCORD_TOKEN") or "").strip()
    if not token:
        raise SystemExit("missing Discord bot token")
    sync_state_file = Path(args.sync_state_file)
    sync_state = load_json_file(sync_state_file, {"version": 1, "channel_id": args.channel_id, "messages": {}})
    sync_state.setdefault("version", 1)
    sync_state.setdefault("messages", {})
    sync_state["channel_id"] = args.channel_id

    desired: list[tuple[str, dict[str, Any], str]] = []
    for chapter in data.get("chapters", []):
        payload = build_chapter_payload(chapter, state, data["pack"]["version_label"])
        payload_hash = hashlib.sha1(json.dumps(payload, sort_keys=True).encode("utf-8")).hexdigest()
        desired.append((chapter["id"], payload, payload_hash))

    existing = sync_state.get("messages", {})
    seen: set[str] = set()
    created = 0
    updated = 0

    for chapter_id, payload, payload_hash in desired:
        seen.add(chapter_id)
        tracked = existing.get(chapter_id, {})
        message_id = str(tracked.get("message_id", "")).strip()
        if message_id and tracked.get("hash") == payload_hash:
            continue
        if message_id:
            try:
                discord_request("PATCH", f"/channels/{args.channel_id}/messages/{message_id}", token, payload)
                updated += 1
            except SystemExit as exc:
                text = str(exc)
                if 'HTTP 429' in text and '"code": 30046' in text:
                    discord_request("DELETE", f"/channels/{args.channel_id}/messages/{message_id}", token, None)
                    raw, _ = discord_request("POST", f"/channels/{args.channel_id}/messages", token, payload)
                    resp = json.loads(raw.decode("utf-8"))
                    message_id = str(resp.get("id", "")).strip()
                    if not message_id:
                        raise SystemExit(f"discord recreate message returned empty id for chapter {chapter_id}") from exc
                    updated += 1
                else:
                    raise
        else:
            raw, _ = discord_request("POST", f"/channels/{args.channel_id}/messages", token, payload)
            resp = json.loads(raw.decode("utf-8"))
            message_id = str(resp.get("id", "")).strip()
            if not message_id:
                raise SystemExit(f"discord create message returned empty id for chapter {chapter_id}")
            created += 1
        existing[chapter_id] = {"message_id": message_id, "hash": payload_hash}

    removed = 0
    for chapter_id in list(existing.keys()):
        if chapter_id in seen:
            continue
        message_id = str(existing[chapter_id].get("message_id", "")).strip()
        if message_id:
            discord_request("DELETE", f"/channels/{args.channel_id}/messages/{message_id}", token, None)
            removed += 1
        del existing[chapter_id]

    sync_state["messages"] = existing
    sync_state["synced_at"] = now_utc()
    save_json_file(sync_state_file, sync_state)
    print(
        f"synced {len(desired)} chapter embeds to channel {args.channel_id} "
        f"(created={created}, updated={updated}, removed={removed})"
    )
    return 0


def cmd_post_message(args: argparse.Namespace) -> int:
    token = (args.bot_token or os.environ.get("DISCORD_BOT_TOKEN") or os.environ.get("GREGGPT_DISCORD_TOKEN") or "").strip()
    if not token:
        raise SystemExit("missing Discord bot token")
    payload = {"content": args.content}
    discord_request("POST", f"/channels/{args.channel_id}/messages", token, payload)
    print(f"posted message to {args.channel_id}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="ATMons questbook tracker")
    sub = p.add_subparsers(dest="command", required=True)

    extract = sub.add_parser("extract", help="extract a questbook snapshot into normalized JSON")
    extract.add_argument("--source-dir", required=True)
    extract.add_argument("--output-file", required=True)
    extract.add_argument("--pack-name", default="all-the-mons")
    extract.add_argument("--display-name", default="All the Mons - ATMons")
    extract.add_argument("--version-label", default=DEFAULT_VERSION)
    extract.add_argument("--release-date", default=DEFAULT_RELEASE_DATE)
    extract.add_argument("--source-repo", default=DEFAULT_SOURCE_REPO)
    extract.add_argument("--source-commit", default=DEFAULT_SOURCE_COMMIT)
    extract.add_argument("--source-note", default=DEFAULT_SOURCE_NOTE)
    extract.set_defaults(func=extract_questbook)

    for name, func in {
        "status": cmd_status,
        "find": cmd_find,
        "show": cmd_show,
        "complete": cmd_complete,
        "reopen": cmd_reopen,
        "render-json": cmd_render_json,
        "sync-channel": cmd_sync_channel,
        "post-message": cmd_post_message,
    }.items():
        sp = sub.add_parser(name)
        if name in {"status", "find", "show", "complete", "reopen", "render-json", "sync-channel"}:
            sp.add_argument("--data-file", default=DEFAULT_DATA_FILE)
            sp.add_argument("--state-file", default=DEFAULT_STATE_FILE)
        if name in {"find", "show", "complete", "reopen"}:
            sp.add_argument("query")
        if name == "find":
            sp.add_argument("--limit", type=int, default=20)
        if name == "complete":
            sp.add_argument("--by", default="GregGPT")
            sp.add_argument("--note", default="")
        if name == "sync-channel":
            sp.add_argument("--channel-id", required=True)
            sp.add_argument("--sync-state-file", default=DEFAULT_SYNC_STATE_FILE)
            sp.add_argument("--bot-token", default="")
        if name == "post-message":
            sp.add_argument("--channel-id", required=True)
            sp.add_argument("--content", required=True)
            sp.add_argument("--bot-token", default="")
        sp.set_defaults(func=func)

    return p


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())
