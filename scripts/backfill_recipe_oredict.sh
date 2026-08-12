#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
db_path=${1:-}
index_path=${2:-$ROOT/workspace/gtnh-data/index/oredict_index.tsv}

if [[ -z "$db_path" || ! -s "$db_path" ]]; then
  echo "usage: $0 /path/to/greggpt_recipes.sqlite [oredict_index.tsv]" >&2
  exit 2
fi
if [[ ! -s "$index_path" ]]; then
  echo "ore-dictionary index is missing or empty: $index_path" >&2
  exit 2
fi
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
backup_path=${BACKUP_PATH:-$db_path.before-oredict-backfill-$(date -u +%Y%m%dT%H%M%SZ)-$$}
if [[ -e "$backup_path" ]]; then
  echo "refusing to overwrite existing backup: $backup_path" >&2
  exit 1
fi
mkdir -p "$(dirname "$backup_path")"
backup_tmp="$backup_path.tmp.$$"
trap 'rm -f "$backup_tmp"' EXIT
python3 - "$db_path" "$backup_tmp" <<'PY'
import sqlite3
import sys

source = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
destination = sqlite3.connect(sys.argv[2])
try:
    source.backup(destination)
finally:
    destination.close()
    source.close()
PY
chmod --reference="$db_path" "$backup_tmp"
if ! ln "$backup_tmp" "$backup_path"; then
  echo "could not publish backup without overwriting an existing file: $backup_path" >&2
  exit 1
fi
rm -f "$backup_tmp"
echo "created consistent pre-patch backup: $backup_path"

python3 - "$db_path" "$index_path" <<'PY'
import csv
import sqlite3
import sys

db_path, index_path = sys.argv[1:3]
db = sqlite3.connect(db_path)
try:
    schema = db.execute("SELECT value FROM manifest WHERE key='schema_version'").fetchone()
    if not schema or schema[0] != "2":
        raise SystemExit("recipe database must use schema version 2")
    table = db.execute(
        "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='ore_dict_entries'"
    ).fetchone()[0]
    if table != 1:
        raise SystemExit("recipe database is missing ore_dict_entries")

    item_ids = {
        (registry, int(damage)): int(item_id)
        for item_id, registry, damage in db.execute(
            "SELECT id, registry_name, damage FROM items"
        )
    }
    exact_rows = set()
    source_rows = 0
    unmatched_rows = 0
    with open(index_path, newline="", encoding="utf-8") as handle:
        reader = csv.DictReader(handle, delimiter="\t")
        required = {"ore_name", "slug", "reg_name"}
        if not required.issubset(reader.fieldnames or ()):
            raise SystemExit("ore-dictionary index has an unexpected header")
        for row in reader:
            source_rows += 1
            ore_name = (row.get("ore_name") or "").strip()
            registry = (row.get("reg_name") or "").strip()
            slug = (row.get("slug") or "").strip()
            if not ore_name or not registry or "d" not in slug:
                unmatched_rows += 1
                continue
            try:
                damage = int(slug.rsplit("d", 1)[1])
            except ValueError:
                unmatched_rows += 1
                continue
            item_id = item_ids.get((registry, damage))
            if item_id is None:
                unmatched_rows += 1
                continue
            exact_rows.add((ore_name, item_id))

    before = db.execute("SELECT count(*) FROM ore_dict_entries").fetchone()[0]
    with db:
        db.executemany(
            "INSERT OR IGNORE INTO ore_dict_entries(ore_name, item_id) VALUES (?, ?)",
            sorted(exact_rows),
        )
        db.execute(
            "CREATE INDEX IF NOT EXISTS idx_ore_dict_entries_item "
            "ON ore_dict_entries(item_id, ore_name)"
        )
        db.execute("ANALYZE ore_dict_entries")
        db.execute("PRAGMA optimize")
    after = db.execute("SELECT count(*) FROM ore_dict_entries").fetchone()[0]
    ore_names = db.execute("SELECT count(DISTINCT ore_name) FROM ore_dict_entries").fetchone()[0]
    print(
        f"backfilled recipe ore dictionary: source_rows={source_rows} "
        f"exact_rows={len(exact_rows)} unmatched_rows={unmatched_rows} "
        f"inserted={after-before} total={after} ore_names={ore_names}"
    )
finally:
    db.close()
PY
