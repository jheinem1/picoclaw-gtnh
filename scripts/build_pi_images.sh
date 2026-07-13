#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLATFORM="${PLATFORM:-linux/arm64}"
OUT_DIR="${OUT_DIR:-$ROOT/build/pi-images}"
DATHOST_BRIDGE_IMAGE_TAR="${DATHOST_BRIDGE_IMAGE_TAR:-$OUT_DIR/deploy_dathost-bridge.tar}"
MC_RELAY_IMAGE_TAR="${MC_RELAY_IMAGE_TAR:-$OUT_DIR/deploy_mc-relay.tar}"
DISCORD_COMMANDS_IMAGE_TAR="${DISCORD_COMMANDS_IMAGE_TAR:-$OUT_DIR/deploy_discord-commands.tar}"
KANBAN_SYNC_IMAGE_TAR="${KANBAN_SYNC_IMAGE_TAR:-$OUT_DIR/deploy_kanban-sync.tar}"
INVENTORY_SYNC_IMAGE_TAR="${INVENTORY_SYNC_IMAGE_TAR:-$OUT_DIR/deploy_inventory-sync.tar}"

"$ROOT/scripts/build_pi_prebuilts.sh"

mkdir -p "$OUT_DIR"

podman build --platform "$PLATFORM" -f "$ROOT/bridge/Dockerfile.prebuilt" -t localhost/deploy_dathost-bridge:latest "$ROOT"
podman build --platform "$PLATFORM" -f "$ROOT/relay/Dockerfile.prebuilt" -t localhost/deploy_mc-relay:latest "$ROOT"
podman build --platform "$PLATFORM" -f "$ROOT/discord-commands/Dockerfile.prebuilt" -t localhost/deploy_discord-commands:latest "$ROOT"
podman build --platform "$PLATFORM" -f "$ROOT/kanban-sync/Dockerfile.prebuilt" -t localhost/deploy_kanban-sync:latest "$ROOT"
podman build --platform "$PLATFORM" -f "$ROOT/inventory-sync/Dockerfile.prebuilt" -t localhost/deploy_inventory-sync:latest "$ROOT"
rm -f "$DATHOST_BRIDGE_IMAGE_TAR" "$MC_RELAY_IMAGE_TAR" "$DISCORD_COMMANDS_IMAGE_TAR" "$KANBAN_SYNC_IMAGE_TAR" "$INVENTORY_SYNC_IMAGE_TAR"
podman save -o "$DATHOST_BRIDGE_IMAGE_TAR" localhost/deploy_dathost-bridge:latest
podman save -o "$MC_RELAY_IMAGE_TAR" localhost/deploy_mc-relay:latest
podman save -o "$DISCORD_COMMANDS_IMAGE_TAR" localhost/deploy_discord-commands:latest
podman save -o "$KANBAN_SYNC_IMAGE_TAR" localhost/deploy_kanban-sync:latest
podman save -o "$INVENTORY_SYNC_IMAGE_TAR" localhost/deploy_inventory-sync:latest

echo "wrote Pi image archives:"
echo "$DATHOST_BRIDGE_IMAGE_TAR"
echo "$MC_RELAY_IMAGE_TAR"
echo "$DISCORD_COMMANDS_IMAGE_TAR"
echo "$KANBAN_SYNC_IMAGE_TAR"
echo "$INVENTORY_SYNC_IMAGE_TAR"
