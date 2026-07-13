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

$SSH_CMD "$PI_HOST" "
set -euo pipefail
mkdir -p '$PI_DIR' '$PI_DATA_DIR'
if [[ -L '$PI_DIR/workspace' ]]; then
  workspace_target=\$(readlink -f '$PI_DIR/workspace')
  if [[ \"\$workspace_target\" != '$PI_DATA_DIR/workspace' ]]; then
    echo \"error: $PI_DIR/workspace points to \$workspace_target, expected $PI_DATA_DIR/workspace\" >&2
    exit 1
  fi
elif [[ -d '$PI_DIR/workspace' ]]; then
  if [[ -d '$PI_DATA_DIR/workspace' ]] && find '$PI_DATA_DIR/workspace' -mindepth 1 -print -quit | grep -q .; then
    echo 'error: both the boot-drive and flash-drive workspace directories contain data; refusing to merge automatically' >&2
    exit 1
  fi
  rmdir '$PI_DATA_DIR/workspace' 2>/dev/null || true
  mv '$PI_DIR/workspace' '$PI_DATA_DIR/workspace'
  ln -s '$PI_DATA_DIR/workspace' '$PI_DIR/workspace'
elif [[ -e '$PI_DIR/workspace' ]]; then
  echo 'error: workspace path exists but is neither a directory nor a symlink' >&2
  exit 1
else
  mkdir -p '$PI_DATA_DIR/workspace'
  ln -s '$PI_DATA_DIR/workspace' '$PI_DIR/workspace'
fi
"

rsync -av --delete --keep-dirlinks \
  -e "$SSH_CMD" \
  --exclude '.git/' \
  --exclude 'build/' \
  --exclude 'runtime/' \
  --exclude 'deploy/env/*.env' \
  --exclude 'workspace/memory/' \
  --exclude 'workspace/sessions/' \
  --exclude 'workspace/state/' \
  --exclude 'workspace/cron/' \
  --exclude 'workspace/HEARTBEAT.md' \
  --exclude 'workspace/heartbeat.log' \
  "$ROOT/" "$PI_HOST:$PI_DIR/"

$SSH_CMD "$PI_HOST" bash -s -- "$PI_DIR" <<'REMOTE'
set -euo pipefail
PI_DIR="$1"
mkdir -p "$PI_DIR/runtime/greggpt"
mkdir -p "$PI_DIR/runtime/dathost-bridge"
mkdir -p "$PI_DIR/runtime/mc-relay"
mkdir -p "$PI_DIR/runtime/kanban-sync"
mkdir -p "$PI_DIR/runtime/inventory-sync"
mkdir -p "$PI_DIR/data/gtnh"
mkdir -p "$PI_DIR/data/gtnh_runtime"
if [[ ! -f "$PI_DIR/deploy/env/greggpt.env" ]]; then
  cp "$PI_DIR/deploy/env/greggpt.env.template" "$PI_DIR/deploy/env/greggpt.env"
fi
if [[ ! -f "$PI_DIR/deploy/env/dathost-bridge.env" ]]; then
  cp "$PI_DIR/deploy/env/dathost-bridge.env.template" "$PI_DIR/deploy/env/dathost-bridge.env"
fi
chmod +x "$PI_DIR"/workspace/gtnh_inventory "$PI_DIR"/workspace/gtnh_wiki_page "$PI_DIR"/workspace/mc_poll "$PI_DIR"/workspace/mc_online "$PI_DIR"/workspace/mc_say "$PI_DIR"/workspace/tools/*.py "$PI_DIR"/workspace/tools/*.sh "$PI_DIR"/scripts/*.sh || true
REMOTE
