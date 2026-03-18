#!/usr/bin/env python3
import collections
import pathlib
import sys

BASE = pathlib.Path(__file__).resolve().parents[2]
DATA_DIR = BASE / "data" / "gtnh"
INDEX_DIR = DATA_DIR / "index"
DUMP_PATH = DATA_DIR / "oredict_dump.tsv"
OUT_PATH = INDEX_DIR / "oredict_index.tsv"
ALIASES: dict[tuple[str, int], tuple[str, int]] = {
    ("TwilightForest:tile.TFCicada", 32767): ("TwilightForest:item.critter", 1),
    ("TwilightForest:tile.TFMoonworm", 32767): ("TwilightForest:item.critter", 2),
    ("OpenComputers:endstone", 0): ("minecraft:end_stone", 0),
}


def default_items_path() -> pathlib.Path:
    candidates = [
        INDEX_DIR / "item_index.tsv",
        BASE / "data" / "gtnh_runtime" / "index" / "item_index.tsv",
        BASE / "workspace" / "gtnh-data" / "index" / "item_index.tsv",
    ]
    for path in candidates:
        if path.exists():
            return path
    return candidates[0]


ITEMS_PATH = default_items_path()


def parse_slug_damage(slug: str) -> int:
    marker = slug.rfind("d")
    if marker <= 0:
        return 0
    suffix = slug[marker + 1 :]
    if suffix and (suffix.isdigit() or (suffix.startswith("-") and suffix[1:].isdigit())):
        return int(suffix)
    return 0


def parse_slug_item_id(slug: str) -> int | None:
    marker = slug.rfind("d")
    if marker > 0:
        prefix = slug[:marker]
        suffix = slug[marker + 1 :]
        if prefix.isdigit() and suffix and (suffix.isdigit() or (suffix.startswith("-") and suffix[1:].isdigit())):
            return int(prefix)
    if slug.isdigit():
        return int(slug)
    return None


def load_items() -> tuple[
    dict[tuple[str, int], list[tuple[str, str, str, str]]],
    dict[str, list[tuple[str, str, str, str]]],
    dict[str, set[int]],
]:
    exact: dict[tuple[str, int], list[tuple[str, str, str, str]]] = collections.defaultdict(list)
    by_reg: dict[str, list[tuple[str, str, str, str]]] = collections.defaultdict(list)
    by_reg_item_ids: dict[str, set[int]] = collections.defaultdict(set)

    with ITEMS_PATH.open("r", encoding="utf-8") as fh:
        header = next(fh, None)
        if header is None:
            raise RuntimeError(f"empty item index: {ITEMS_PATH}")
        for line in fh:
            line = line.rstrip("\n")
            if not line:
                continue
            parts = line.split("\t")
            if len(parts) < 4:
                continue
            slug, display_name, reg_name, _name = parts[:4]
            damage = parse_slug_damage(slug)
            item_id = parse_slug_item_id(slug)
            record = (slug, display_name, reg_name, _name)
            exact[(reg_name, damage)].append(record)
            by_reg[reg_name].append(record)
            if item_id is not None:
                by_reg_item_ids[reg_name].add(item_id)

    return exact, by_reg, by_reg_item_ids


def main() -> int:
    for path in (DUMP_PATH, ITEMS_PATH):
        if not path.exists():
            print(f"missing: {path}", file=sys.stderr)
            return 1

    exact, by_reg, by_reg_item_ids = load_items()
    matched_rows: set[tuple[str, str, str, str, str]] = set()
    unresolved = 0
    synthetic = 0

    with DUMP_PATH.open("r", encoding="utf-8") as fh:
        header = next(fh, None)
        if header is None:
            print(f"empty dump: {DUMP_PATH}", file=sys.stderr)
            return 1
        for line in fh:
            line = line.rstrip("\n")
            if not line:
                continue
            parts = line.split("\t")
            if len(parts) < 4:
                continue
            ore_name, reg_name, damage_raw, _display_name = parts[:4]
            try:
                damage = int(damage_raw)
            except ValueError:
                unresolved += 1
                continue

            candidates: list[tuple[str, str, str, str]]
            if damage in (-1, 32767):
                candidates = by_reg.get(reg_name, [])
            else:
                candidates = exact.get((reg_name, damage), [])
                if not candidates:
                    alias = ALIASES.get((reg_name, damage))
                    if alias is not None:
                        candidates = exact.get(alias, [])

            if not candidates:
                item_ids = by_reg_item_ids.get(reg_name, set())
                if damage not in (-1, 32767) and len(item_ids) == 1:
                    item_id = next(iter(item_ids))
                    fallback = by_reg.get(reg_name, [])
                    fallback_display = _display_name if _display_name and _display_name != "invalid.name" else (fallback[0][1] if fallback else "")
                    fallback_name = fallback_display
                    matched_rows.add(
                        (ore_name, f"{item_id}d{damage}", fallback_display, reg_name, fallback_name)
                    )
                    synthetic += 1
                    continue
                unresolved += 1
                continue

            for slug, display_name, item_reg_name, item_name in candidates:
                matched_rows.add((ore_name, slug, display_name, item_reg_name, item_name))

    OUT_PATH.parent.mkdir(parents=True, exist_ok=True)
    with OUT_PATH.open("w", encoding="utf-8") as fh:
        fh.write("ore_name\tslug\tdisplay_name\treg_name\tname\n")
        for ore_name, slug, display_name, reg_name, item_name in sorted(
            matched_rows, key=lambda row: (row[0].lower(), row[2].lower(), row[1])
        ):
            fh.write(f"{ore_name}\t{slug}\t{display_name}\t{reg_name}\t{item_name}\n")

    print(f"wrote: {OUT_PATH}")
    print(f"rows: {len(matched_rows)}")
    print(f"synthetic_rows: {synthetic}")
    print(f"unresolved_dump_rows: {unresolved}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
