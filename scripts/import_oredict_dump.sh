#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DUMP_SOURCE="${1:-}"
DUMP_DEST="$ROOT/data/gtnh/oredict_dump.tsv"

if [[ -z "$DUMP_SOURCE" ]]; then
  echo "usage: scripts/import_oredict_dump.sh <path-to-greggpt_oredict_dump.tsv>" >&2
  exit 1
fi
if [[ ! -f "$DUMP_SOURCE" ]]; then
  echo "missing dump: $DUMP_SOURCE" >&2
  exit 1
fi

mkdir -p "$ROOT/data/gtnh"
cp -f "$DUMP_SOURCE" "$DUMP_DEST"
"$ROOT/workspace/tools/build_oredict_index.py"
"$ROOT/scripts/prepare_runtime_data.sh"

echo "imported: $DUMP_DEST"
