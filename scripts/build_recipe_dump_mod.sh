#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/scripts/recipe_dump/GregGPTRecipeDumpMod.java"
BUILD_DIR="$ROOT/build/recipe-dump-mod"
CLASSES_DIR="$BUILD_DIR/classes"
OUT_JAR="$BUILD_DIR/greggpt-recipe-dump-2.0.0.jar"
FORGE_JAR="${FORGE_JAR:-$HOME/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/libraries/net/minecraftforge/forge/1.7.10-10.13.4.1614-1.7.10/forge-1.7.10-10.13.4.1614-1.7.10-universal.jar}"
SQLITE_JDBC_JAR="${SQLITE_JDBC_JAR:-}"
JAVAC_BIN="${JAVAC_BIN:-$HOME/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/java/java-runtime-gamma/bin/javac}"
JAR_BIN="${JAR_BIN:-$HOME/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/java/java-runtime-gamma/bin/jar}"

if [[ ! -f "$SRC" ]]; then
  echo "missing source: $SRC" >&2
  exit 1
fi
if [[ ! -f "$FORGE_JAR" ]]; then
  echo "missing forge jar: $FORGE_JAR" >&2
  exit 1
fi
if [[ -n "$SQLITE_JDBC_JAR" && ! -f "$SQLITE_JDBC_JAR" ]]; then
  echo "missing sqlite jdbc jar: $SQLITE_JDBC_JAR" >&2
  exit 1
fi
if [[ ! -x "$JAVAC_BIN" ]]; then
  JAVAC_BIN="$(command -v javac)"
fi
if [[ ! -x "$JAR_BIN" ]]; then
  JAR_BIN="$(command -v jar)"
fi

mkdir -p "$CLASSES_DIR"
rm -rf "$CLASSES_DIR"/*

CP="$FORGE_JAR"
if [[ -n "$SQLITE_JDBC_JAR" ]]; then
  CP="$CP:$SQLITE_JDBC_JAR"
fi

"$JAVAC_BIN" --release 8 -cp "$CP" -d "$CLASSES_DIR" "$SRC"

(
  cd "$CLASSES_DIR"
  "$JAR_BIN" cf "$OUT_JAR" .
)

echo "wrote: $OUT_JAR"
