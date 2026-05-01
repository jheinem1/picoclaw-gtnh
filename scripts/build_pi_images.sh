#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLATFORM="${PLATFORM:-linux/arm64}"
OUT_DIR="${OUT_DIR:-$ROOT/build/pi-images}"
MC_RELAY_IMAGE_TAR="${MC_RELAY_IMAGE_TAR:-$OUT_DIR/deploy_mc-relay.tar}"
DISCORD_COMMANDS_IMAGE_TAR="${DISCORD_COMMANDS_IMAGE_TAR:-$OUT_DIR/deploy_discord-commands.tar}"

"$ROOT/scripts/build_pi_prebuilts.sh"

mkdir -p "$OUT_DIR"

podman build --platform "$PLATFORM" -f "$ROOT/relay/Dockerfile.prebuilt" -t localhost/deploy_mc-relay:latest "$ROOT"
podman build --platform "$PLATFORM" -f "$ROOT/discord-commands/Dockerfile.prebuilt" -t localhost/deploy_discord-commands:latest "$ROOT"
rm -f "$MC_RELAY_IMAGE_TAR" "$DISCORD_COMMANDS_IMAGE_TAR"
podman save -o "$MC_RELAY_IMAGE_TAR" localhost/deploy_mc-relay:latest
podman save -o "$DISCORD_COMMANDS_IMAGE_TAR" localhost/deploy_discord-commands:latest

echo "wrote Pi image archives:"
echo "$MC_RELAY_IMAGE_TAR"
echo "$DISCORD_COMMANDS_IMAGE_TAR"
