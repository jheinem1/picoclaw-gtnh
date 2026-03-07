#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_DIR="${PI_DIR:-/home/jhein/picoclaw-gtnh}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"

rsync -av --delete \
  -e "$SSH_CMD" \
  --exclude '.git/' \
  --exclude 'runtime/' \
  --exclude 'deploy/env/picoclaw.env' \
  --exclude 'deploy/env/dathost-bridge.env' \
  --exclude 'workspace/memory/' \
  --exclude 'workspace/sessions/' \
  --exclude 'workspace/state/' \
  --exclude 'workspace/cron/' \
  --exclude 'workspace/HEARTBEAT.md' \
  --exclude 'workspace/heartbeat.log' \
  "$ROOT/" "$PI_HOST:$PI_DIR/"

$SSH_CMD "$PI_HOST" '
set -euo pipefail
mkdir -p /home/jhein/picoclaw-gtnh/runtime/picoclaw
mkdir -p /home/jhein/picoclaw-gtnh/runtime/dathost-bridge
mkdir -p /home/jhein/picoclaw-gtnh/runtime/mc-relay
mkdir -p /home/jhein/picoclaw-gtnh/runtime/inventory-sync
mkdir -p /home/jhein/picoclaw-gtnh/data/gtnh
mkdir -p /home/jhein/picoclaw-gtnh/data/gtnh_runtime
if [[ ! -f /home/jhein/picoclaw-gtnh/deploy/env/picoclaw.env ]]; then
  cp /home/jhein/picoclaw-gtnh/deploy/env/picoclaw.env.template /home/jhein/picoclaw-gtnh/deploy/env/picoclaw.env
fi
if [[ ! -f /home/jhein/picoclaw-gtnh/runtime/picoclaw/config.json ]]; then
  cp /home/jhein/picoclaw-gtnh/deploy/config/picoclaw.config.template.json /home/jhein/picoclaw-gtnh/runtime/picoclaw/config.json
fi
if [[ ! -f /home/jhein/picoclaw-gtnh/deploy/env/dathost-bridge.env ]]; then
  cp /home/jhein/picoclaw-gtnh/deploy/env/dathost-bridge.env.template /home/jhein/picoclaw-gtnh/deploy/env/dathost-bridge.env
fi
chmod +x /home/jhein/picoclaw-gtnh/workspace/gtnh_query /home/jhein/picoclaw-gtnh/workspace/gtnh_inventory /home/jhein/picoclaw-gtnh/workspace/mc_poll /home/jhein/picoclaw-gtnh/workspace/mc_online /home/jhein/picoclaw-gtnh/workspace/mc_say /home/jhein/picoclaw-gtnh/workspace/tools/*.py /home/jhein/picoclaw-gtnh/workspace/tools/*.sh /home/jhein/picoclaw-gtnh/scripts/*.sh || true
'
