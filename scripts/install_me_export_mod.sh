#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_JAR="${MOD_JAR:-$ROOT/build/me-export-mod/greggpt-me-export-1.0.0.jar}"
SERVER_DIR="${1:-}"

if [[ -z "$SERVER_DIR" ]]; then
  echo "usage: scripts/install_me_export_mod.sh <minecraft-server-dir>" >&2
  exit 1
fi
if [[ ! -f "$MOD_JAR" ]]; then
  echo "missing mod jar: $MOD_JAR" >&2
  echo "run scripts/build_me_export_mod.sh first" >&2
  exit 1
fi

MODS_DIR="$SERVER_DIR/mods"
mkdir -p "$MODS_DIR"
cp -f "$MOD_JAR" "$MODS_DIR/greggpt-me-export-1.0.0.jar"

echo "installed: $MODS_DIR/greggpt-me-export-1.0.0.jar"
echo "after the server starts, look for world/greggpt/me_index.json"
