#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="${SOURCE_DIR:-$HOME/Downloads}"
DEST_DIR="$ROOT/data/gtnh"
REMOTE_TARGET="${REMOTE_TARGET:-jhein@192.168.1.59:/home/jhein/picoclaw-gtnh/data/gtnh}"
REMOTE_RUNTIME_TARGET="${REMOTE_RUNTIME_TARGET:-jhein@192.168.1.59:/home/jhein/picoclaw-gtnh/data/gtnh_runtime}"
DEPLOY_TO_PI="${DEPLOY_TO_PI:-0}"
RESTART_PI_SERVICE="${RESTART_PI_SERVICE:-0}"
PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"

MANDATORY=(
  "recipes.json"
  "recipes_stacks.json"
)

OPTIONAL=(
  "recipes.tar.gz"
  "notenoughrecipedumps-v1.0-beta.jar"
  "gtnhlib-0.8.15.jar"
)

mkdir -p "$DEST_DIR"

copy_if_exists() {
  local f="$1"
  if [[ -f "$SOURCE_DIR/$f" ]]; then
    cp -f "$SOURCE_DIR/$f" "$DEST_DIR/$f"
    echo "copied: $f"
  else
    echo "missing (skipped): $f" >&2
  fi
}

for f in "${MANDATORY[@]}"; do
  if [[ ! -f "$SOURCE_DIR/$f" ]]; then
    echo "required file missing: $SOURCE_DIR/$f" >&2
    exit 1
  fi
  copy_if_exists "$f"
done

for f in "${OPTIONAL[@]}"; do
  copy_if_exists "$f"
done

"$ROOT/workspace/tools/build_item_index.py"
"$ROOT/workspace/tools/build_recipe_index.py"
"$ROOT/scripts/prepare_runtime_data.sh"

if [[ "$DEPLOY_TO_PI" == "1" ]]; then
  rsync -av --delete -e "$SSH_CMD" "$DEST_DIR/" "$REMOTE_TARGET/"
  rsync -av --delete -e "$SSH_CMD" "$ROOT/data/gtnh_runtime/" "$REMOTE_RUNTIME_TARGET/"
  echo "synced to pi: $REMOTE_TARGET"

  if [[ "$RESTART_PI_SERVICE" == "1" ]]; then
    $SSH_CMD "$PI_HOST" 'systemctl --user restart picoclaw-gtnh.service && systemctl --user --no-pager --full status picoclaw-gtnh.service | sed -n "1,80p"'
  fi
fi
