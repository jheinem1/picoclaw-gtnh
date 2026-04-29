#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/scripts/oredict_dump/GregGPTOreDictDumpMod.java"
BUILD_DIR="$ROOT/build/oredict-mod"
CLASSES_DIR="$BUILD_DIR/classes"
OUT_JAR="$BUILD_DIR/greggpt-oredict-dump-1.0.0.jar"
FORGE_JAR="${FORGE_JAR:-$HOME/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/libraries/net/minecraftforge/forge/1.7.10-10.13.4.1614-1.7.10/forge-1.7.10-10.13.4.1614-1.7.10-universal.jar}"
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
if [[ ! -x "$JAVAC_BIN" ]]; then
  JAVAC_BIN="$(command -v javac)"
fi
if [[ ! -x "$JAR_BIN" ]]; then
  JAR_BIN="$(command -v jar)"
fi

mkdir -p "$CLASSES_DIR"
rm -rf "$CLASSES_DIR"/*

"$JAVAC_BIN" --release 8 -cp "$FORGE_JAR" -d "$CLASSES_DIR" "$SRC"

(
  cd "$CLASSES_DIR"
  "$JAR_BIN" cf "$OUT_JAR" .
)

echo "wrote: $OUT_JAR"
