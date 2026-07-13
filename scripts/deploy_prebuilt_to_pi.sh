#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_DIR="${PI_DIR:-/home/jhein/greggpt-gtnh}"
PI_DATA_DIR="${PI_DATA_DIR:-/home/jhein/greggpt-data}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"
DATHOST_BRIDGE_IMAGE_TAR="${DATHOST_BRIDGE_IMAGE_TAR:-$ROOT/build/pi-images/deploy_dathost-bridge.tar}"
MC_RELAY_IMAGE_TAR="${MC_RELAY_IMAGE_TAR:-$ROOT/build/pi-images/deploy_mc-relay.tar}"
DISCORD_COMMANDS_IMAGE_TAR="${DISCORD_COMMANDS_IMAGE_TAR:-$ROOT/build/pi-images/deploy_discord-commands.tar}"
KANBAN_SYNC_IMAGE_TAR="${KANBAN_SYNC_IMAGE_TAR:-$ROOT/build/pi-images/deploy_kanban-sync.tar}"
INVENTORY_SYNC_IMAGE_TAR="${INVENTORY_SYNC_IMAGE_TAR:-$ROOT/build/pi-images/deploy_inventory-sync.tar}"

DATHOST_BRIDGE_IMAGE_TAR="$DATHOST_BRIDGE_IMAGE_TAR" \
MC_RELAY_IMAGE_TAR="$MC_RELAY_IMAGE_TAR" \
DISCORD_COMMANDS_IMAGE_TAR="$DISCORD_COMMANDS_IMAGE_TAR" \
KANBAN_SYNC_IMAGE_TAR="$KANBAN_SYNC_IMAGE_TAR" \
INVENTORY_SYNC_IMAGE_TAR="$INVENTORY_SYNC_IMAGE_TAR" \
  "$ROOT/scripts/build_pi_images.sh"
PI_HOST="$PI_HOST" PI_DIR="$PI_DIR" PI_DATA_DIR="$PI_DATA_DIR" PI_PUBKEY="$PI_PUBKEY" \
  "$ROOT/scripts/deploy_to_pi.sh"
PI_HOST="$PI_HOST" PI_DIR="$PI_DIR" PI_PUBKEY="$PI_PUBKEY" \
  "$ROOT/scripts/install_user_service.sh"
REMOTE_IMAGE_DIR="$PI_DATA_DIR/prebuilt-images"
$SSH_CMD "$PI_HOST" "mkdir -p '$REMOTE_IMAGE_DIR'"
rsync -av -e "$SSH_CMD" \
  "$DATHOST_BRIDGE_IMAGE_TAR" \
  "$MC_RELAY_IMAGE_TAR" \
  "$DISCORD_COMMANDS_IMAGE_TAR" \
  "$KANBAN_SYNC_IMAGE_TAR" \
  "$INVENTORY_SYNC_IMAGE_TAR" \
  "$PI_HOST:$REMOTE_IMAGE_DIR/"

$SSH_CMD "$PI_HOST" "
set -euo pipefail
graphroot=\$(podman info --format '{{.Store.GraphRoot}}')
if [[ \"\$graphroot\" != '$PI_DATA_DIR/containers/storage' ]]; then
  echo \"error: Podman graphroot is \$graphroot; expected $PI_DATA_DIR/containers/storage\" >&2
  echo \"run scripts/setup_pi_runtime.sh after safely clearing or migrating the old rootless Podman store\" >&2
  exit 1
fi
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$DATHOST_BRIDGE_IMAGE_TAR")'
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$MC_RELAY_IMAGE_TAR")'
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$DISCORD_COMMANDS_IMAGE_TAR")'
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$KANBAN_SYNC_IMAGE_TAR")'
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$INVENTORY_SYNC_IMAGE_TAR")'
cd '$PI_DIR/deploy'
systemctl --user restart greggpt-gtnh.service
systemctl --user --no-pager --full status greggpt-gtnh.service | sed -n '1,80p'
podman ps --format '{{.Names}} {{.Status}}'
"
