#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT_DIR="$ROOT/mods/ftbquests-dump"
BUILD_DIR="$ROOT/build/ftbquests-dump-mod"
OUT_JAR="$BUILD_DIR/picoclaw-ftbquests-dump-1.0.0.jar"

GRADLE_VERSION="${GRADLE_VERSION:-9.2.1}"
GRADLE_ROOT="$ROOT/.gradle-bin"
GRADLE_DIR="$GRADLE_ROOT/gradle-$GRADLE_VERSION"
GRADLE_BIN="$GRADLE_DIR/bin/gradle"

if [[ ! -d "$PROJECT_DIR" ]]; then
  echo "missing project dir: $PROJECT_DIR" >&2
  exit 1
fi

if [[ ! -x "$GRADLE_BIN" ]]; then
  mkdir -p "$GRADLE_ROOT"
  TMP_ZIP="$GRADLE_ROOT/gradle-$GRADLE_VERSION-bin.zip"
  if [[ ! -f "$TMP_ZIP" ]]; then
    curl -fsSL "https://services.gradle.org/distributions/gradle-$GRADLE_VERSION-bin.zip" -o "$TMP_ZIP"
  fi
  rm -rf "$GRADLE_DIR"
  unzip -q "$TMP_ZIP" -d "$GRADLE_ROOT"
fi

mkdir -p "$BUILD_DIR"
"$GRADLE_BIN" --no-daemon -p "$PROJECT_DIR" clean jar
cp -f "$PROJECT_DIR/build/libs/picoclaw_quest_dump-1.0.0.jar" "$OUT_JAR"

echo "wrote: $OUT_JAR"
