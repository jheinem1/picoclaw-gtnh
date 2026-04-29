#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_JAR="${MOD_JAR:-$ROOT/build/oredict-mod/greggpt-oredict-dump-1.0.0.jar}"
INSTANCE_DIR="${1:-}"

if [[ -z "$INSTANCE_DIR" ]]; then
  echo "usage: scripts/install_oredict_dump_mod.sh <prismlauncher-instance-minecraft-dir>" >&2
  exit 1
fi
if [[ ! -f "$MOD_JAR" ]]; then
  echo "missing mod jar: $MOD_JAR" >&2
  echo "run scripts/build_oredict_dump_mod.sh first" >&2
  exit 1
fi

MODS_DIR="$INSTANCE_DIR/mods"
mkdir -p "$MODS_DIR"
cp -f "$MOD_JAR" "$MODS_DIR/greggpt-oredict-dump-1.0.0.jar"

echo "installed: $MODS_DIR/greggpt-oredict-dump-1.0.0.jar"
echo "after the next GTNH launch, look for dumps/greggpt_oredict_dump.tsv inside that instance"
