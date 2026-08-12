#!/usr/bin/env python3
"""Build NEI-like secondary item names keyed by registry name and damage."""

import argparse
import csv
import pathlib
import re
import subprocess


COLOR = re.compile(r"§[0-9A-FK-ORa-fk-or]")
ENUM_FIELD = re.compile(
    r"public static final binnie\.botany\.genetics\.EnumFlowerColor ([A-Z0-9_]+);"
)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("item_index", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("--binnie-jar", type=pathlib.Path)
    args = parser.parse_args()

    rows: set[tuple[str, int, str, str]] = set()
    with args.item_index.open(newline="", encoding="utf-8") as source:
        for row in csv.reader(source, delimiter="\t"):
            if len(row) < 4 or row[0] == "slug":
                continue
            match = re.fullmatch(r"(\d+)d(-?\d+)", row[0])
            if not match:
                continue
            stripped = COLOR.sub("", row[1]).strip()
            if stripped and stripped != row[1]:
                rows.add((row[2], int(match.group(2)), stripped, "canonical_display_stripped"))

    if args.binnie_jar:
        output = subprocess.check_output(
            [
                "javap",
                "-classpath",
                str(args.binnie_jar),
                "-p",
                "binnie.botany.genetics.EnumFlowerColor",
            ],
            text=True,
        )
        names = [match.group(1) for match in map(ENUM_FIELD.search, output.splitlines()) if match]
        if len(names) != 80:
            raise SystemExit(f"expected 80 Botany colors, found {len(names)}")
        for damage, name in enumerate(names):
            alias = name.replace("_", " ").title()
            rows.add(("Botany:pigment", damage, alias, "binnie.botany.EnumFlowerColor tooltip"))

    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_suffix(args.output.suffix + ".tmp")
    with temporary.open("w", newline="", encoding="utf-8") as target:
        writer = csv.writer(target, delimiter="\t", lineterminator="\n")
        writer.writerow(("registry_name", "damage", "alias", "source"))
        writer.writerows(sorted(rows, key=lambda row: (row[0].lower(), row[1], row[2].lower())))
    temporary.replace(args.output)
    print(f"wrote {len(rows)} item aliases to {args.output}")


if __name__ == "__main__":
    main()
