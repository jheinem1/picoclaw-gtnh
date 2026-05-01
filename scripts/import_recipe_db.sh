#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DB="${1:-${SOURCE_DB:-}}"
DEST_DB="$ROOT/data/gtnh/index/greggpt_recipes.sqlite"

if [[ -z "$SOURCE_DB" ]]; then
  echo "usage: scripts/import_recipe_db.sh <path-to-greggpt_recipes.sqlite>" >&2
  echo "or set SOURCE_DB=/path/to/greggpt_recipes.sqlite" >&2
  exit 2
fi
if [[ ! -s "$SOURCE_DB" ]]; then
  echo "missing recipe sqlite dump: $SOURCE_DB" >&2
  exit 1
fi

mkdir -p "$(dirname "$DEST_DB")"
tmp="$(mktemp "$DEST_DB.tmp.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cp -f "$SOURCE_DB" "$tmp"

if [[ ! -s "$tmp" ]]; then
  echo "recipe database copy is empty: $tmp" >&2
  exit 1
fi

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$tmp" "PRAGMA integrity_check;" | grep -qx "ok"
  for table in manifest items fluids recipe_handlers recipes recipe_inputs recipe_input_options recipe_outputs recipe_metadata; do
    sqlite3 "$tmp" "SELECT 1 FROM $table LIMIT 1;" >/dev/null
  done
fi

mv -f "$tmp" "$DEST_DB"
trap - EXIT
echo "imported recipe database: $DEST_DB"
