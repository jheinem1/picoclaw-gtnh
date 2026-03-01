#!/usr/bin/env python3
import json
import pathlib
import sys

BASE = pathlib.Path(__file__).resolve().parents[2]
STACKS = BASE / "data" / "gtnh" / "recipes_stacks.json"
OUTDIR = BASE / "data" / "gtnh" / "index"
OUT = OUTDIR / "item_index.tsv"


def main() -> int:
    if not STACKS.exists():
        print(f"missing: {STACKS}", file=sys.stderr)
        return 1

    with STACKS.open("r", encoding="utf-8") as fh:
        data = json.load(fh)

    items = data.get("items", {})
    OUTDIR.mkdir(parents=True, exist_ok=True)

    with OUT.open("w", encoding="utf-8") as fh:
        fh.write("slug\tdisplay_name\treg_name\tname\n")
        for slug, meta in items.items():
            display = str(meta.get("displayName", "")).replace("\t", " ").strip()
            reg = str(meta.get("regName", "")).replace("\t", " ").strip()
            name = str(meta.get("name", "")).replace("\t", " ").strip()
            fh.write(f"{slug}\t{display}\t{reg}\t{name}\n")

    print(f"wrote: {OUT}")
    print(f"rows: {len(items)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
