#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD_JAR="${MOD_JAR:-$ROOT/build/recipe-dump-mod/greggpt-recipe-dump-2.0.0.jar}"
SQLITE_JDBC_JAR="${SQLITE_JDBC_JAR:-}"
INSTANCE_DIR="${1:-}"

if [[ -z "$INSTANCE_DIR" ]]; then
  echo "usage: scripts/install_recipe_dump_mod.sh <prismlauncher-instance-minecraft-dir>" >&2
  exit 1
fi
if [[ ! -f "$MOD_JAR" ]]; then
  echo "missing mod jar: $MOD_JAR" >&2
  echo "run scripts/build_recipe_dump_mod.sh first" >&2
  exit 1
fi
if [[ -n "$SQLITE_JDBC_JAR" && ! -f "$SQLITE_JDBC_JAR" ]]; then
  echo "missing sqlite jdbc jar: $SQLITE_JDBC_JAR" >&2
  exit 1
fi

MODS_DIR="$INSTANCE_DIR/mods"
mkdir -p "$MODS_DIR"
rm -f "$MODS_DIR/greggpt-recipe-dump-1.0.0.jar"
cp -f "$MOD_JAR" "$MODS_DIR/greggpt-recipe-dump-2.0.0.jar"

echo "installed: $MODS_DIR/greggpt-recipe-dump-2.0.0.jar"

if [[ -n "$SQLITE_JDBC_JAR" ]]; then
  SQLITE_BASENAME="$(basename "$SQLITE_JDBC_JAR")"
  cp -f "$SQLITE_JDBC_JAR" "$MODS_DIR/$SQLITE_BASENAME"
  echo "installed sqlite jdbc runtime: $MODS_DIR/$SQLITE_BASENAME"
else
  echo "SQLITE_JDBC_JAR not set; sqlite-jdbc must already be available on the runtime classpath"
fi

echo "after the next GTNH launch, look for dumps/greggpt_recipes.sqlite inside that instance"
