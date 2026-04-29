#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_JAR="${MOD_JAR:-$ROOT/build/ftbquests-dump-mod/greggpt-ftbquests-dump-1.0.0.jar}"
INSTANCE_DIR="${1:-}"

if [[ -z "$INSTANCE_DIR" ]]; then
  echo "usage: scripts/install_ftbquests_dump_mod.sh <prismlauncher-instance-minecraft-dir>" >&2
  exit 1
fi
if [[ ! -f "$MOD_JAR" ]]; then
  echo "missing mod jar: $MOD_JAR" >&2
  echo "run scripts/build_ftbquests_dump_mod.sh first" >&2
  exit 1
fi

MODS_DIR="$INSTANCE_DIR/mods"
mkdir -p "$MODS_DIR"
cp -f "$MOD_JAR" "$MODS_DIR/greggpt-ftbquests-dump-1.0.0.jar"

echo "installed: $MODS_DIR/greggpt-ftbquests-dump-1.0.0.jar"
echo "after joining the server, look for dumps/greggpt_ftbquests_snapshot.json and dumps/greggpt_ftbquests_completed.json"
