#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PI_HOST="${PI_HOST:-jhein@192.168.1.41}"
PI_DIR="${PI_DIR:-/home/jhein/greggpt-gtnh}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"

rsync -av --delete \
  -e "$SSH_CMD" \
  --exclude '.git/' \
  --exclude 'runtime/' \
  --exclude 'deploy/env/*.env' \
  --exclude 'workspace/memory/' \
  --exclude 'workspace/sessions/' \
  --exclude 'workspace/state/' \
  --exclude 'workspace/cron/' \
  --exclude 'workspace/HEARTBEAT.md' \
  --exclude 'workspace/heartbeat.log' \
  "$ROOT/" "$PI_HOST:$PI_DIR/"

$SSH_CMD "$PI_HOST" '
set -euo pipefail
mkdir -p /home/jhein/greggpt-gtnh/runtime/greggpt
mkdir -p /home/jhein/greggpt-gtnh/runtime/dathost-bridge
mkdir -p /home/jhein/greggpt-gtnh/runtime/mc-relay
mkdir -p /home/jhein/greggpt-gtnh/runtime/inventory-sync
mkdir -p /home/jhein/greggpt-gtnh/data/gtnh
mkdir -p /home/jhein/greggpt-gtnh/data/gtnh_runtime
if [[ ! -f /home/jhein/greggpt-gtnh/deploy/env/greggpt.env ]]; then
  cp /home/jhein/greggpt-gtnh/deploy/env/greggpt.env.template /home/jhein/greggpt-gtnh/deploy/env/greggpt.env
fi
if [[ ! -f /home/jhein/greggpt-gtnh/deploy/env/dathost-bridge.env ]]; then
  cp /home/jhein/greggpt-gtnh/deploy/env/dathost-bridge.env.template /home/jhein/greggpt-gtnh/deploy/env/dathost-bridge.env
fi
chmod +x /home/jhein/greggpt-gtnh/workspace/gtnh_query /home/jhein/greggpt-gtnh/workspace/gtnh_inventory /home/jhein/greggpt-gtnh/workspace/mc_poll /home/jhein/greggpt-gtnh/workspace/mc_online /home/jhein/greggpt-gtnh/workspace/mc_say /home/jhein/greggpt-gtnh/workspace/tools/*.py /home/jhein/greggpt-gtnh/workspace/tools/*.sh /home/jhein/greggpt-gtnh/scripts/*.sh || true
'
