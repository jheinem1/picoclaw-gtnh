#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CODEX_VERSION="${CODEX_VERSION:-0.147.0}"
CODEX_TARGET="${CODEX_TARGET:-aarch64-unknown-linux-musl}"
OUT="${CODEX_PREBUILT_OUT:-$ROOT/discord-commands/prebuilt/codex}"

case "$CODEX_VERSION/$CODEX_TARGET" in
  0.147.0/aarch64-unknown-linux-musl)
    SHA256="eb677c80f666b1ab8b4b1d083b66e8d614b1281d960bb6f9fd8ca98f58b38b90"
    ;;
  *)
    echo "error: no pinned checksum for Codex $CODEX_VERSION ($CODEX_TARGET)" >&2
    exit 1
    ;;
esac

ASSET="codex-$CODEX_TARGET.tar.gz"
URL="https://github.com/openai/codex/releases/download/rust-v$CODEX_VERSION/$ASSET"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$(dirname "$OUT")"
curl --fail --location --silent --show-error "$URL" --output "$TMP_DIR/$ASSET"
printf '%s  %s\n' "$SHA256" "$TMP_DIR/$ASSET" | sha256sum --check --status
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"
install -m 0755 "$TMP_DIR/codex-$CODEX_TARGET" "$OUT"

echo "wrote Codex $CODEX_VERSION ($CODEX_TARGET) -> $OUT"
