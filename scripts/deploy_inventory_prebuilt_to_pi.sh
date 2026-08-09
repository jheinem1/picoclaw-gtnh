#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_DIR="${PI_DIR:-/home/jhein/greggpt-gtnh}"
PI_DATA_DIR="${PI_DATA_DIR:-/home/jhein/picoclaw-data}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
INVENTORY_SYNC_IMAGE_TAR="${INVENTORY_SYNC_IMAGE_TAR:-$ROOT/build/pi-images/deploy_inventory-sync.tar}"

if [[ ! -f "$INVENTORY_SYNC_IMAGE_TAR" ]]; then
  echo "error: missing inventory-sync image archive: $INVENTORY_SYNC_IMAGE_TAR" >&2
  exit 1
fi

SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"
REMOTE_IMAGE_DIR="$PI_DATA_DIR/prebuilt-images"

$SSH_CMD "$PI_HOST" "mkdir -p '$REMOTE_IMAGE_DIR'"
rsync -av --checksum-choice=sha1 -e "$SSH_CMD" \
  "$INVENTORY_SYNC_IMAGE_TAR" \
  "$PI_HOST:$REMOTE_IMAGE_DIR/"

$SSH_CMD "$PI_HOST" "
set -euo pipefail
graphroot=\$(podman info --format '{{.Store.GraphRoot}}')
if [[ \"\$graphroot\" != '$PI_DATA_DIR/containers/storage' ]]; then
  echo \"error: Podman graphroot is \$graphroot; expected $PI_DATA_DIR/containers/storage\" >&2
  exit 1
fi
podman load -i '$REMOTE_IMAGE_DIR/$(basename "$INVENTORY_SYNC_IMAGE_TAR")'
cd '$PI_DIR/deploy'
podman-compose --env-file env/greggpt.env -f compose.yaml up -d --no-build --force-recreate inventory-sync
podman inspect inventory-sync --format 'inventory-sync image={{.ImageName}} status={{.State.Status}} started={{.State.StartedAt}}'
podman ps --filter name=inventory-sync --format '{{.Names}} {{.Status}} {{.Image}}'
"
