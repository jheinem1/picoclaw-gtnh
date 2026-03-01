#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/data/gtnh"
DST="$ROOT/data/gtnh_runtime"
WS_DST="$ROOT/workspace/gtnh-data"

mkdir -p "$DST/index"
mkdir -p "$WS_DST/index"

for f in item_index.tsv recipe_index.tsv; do
  if [[ ! -f "$SRC/index/$f" ]]; then
    echo "missing required index: $SRC/index/$f" >&2
    exit 1
  fi
  cp -f "$SRC/index/$f" "$DST/index/$f"
  cp -f "$SRC/index/$f" "$WS_DST/index/$f"
done

cat > "$DST/README.txt" <<'EOF'
Runtime GTNH dataset for PicoClaw.

This directory intentionally excludes large raw dumps (recipes.json, recipes_stacks.json)
to prevent accidental full-file reads and OOM/restarts.

Use indexed files under index/:
- item_index.tsv
- recipe_index.tsv
EOF

cat > "$WS_DST/README.txt" <<'EOF'
Workspace runtime GTNH indexes.
Do not add full raw dumps here.
EOF

echo "prepared runtime dataset: $DST"
