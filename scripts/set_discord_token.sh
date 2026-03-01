#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <discord_bot_token>" >&2
  exit 1
fi

TOKEN="$1"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/deploy/env/picoclaw.env"
PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"

cp -n "$ROOT/deploy/env/picoclaw.env.template" "$ENV_FILE" || true
perl -0pi -e 's#PICOCLAW_CHANNELS_DISCORD_TOKEN=.*#PICOCLAW_CHANNELS_DISCORD_TOKEN='"$TOKEN"'#' "$ENV_FILE"

$SSH_CMD "$PI_HOST" 'mkdir -p /home/jhein/picoclaw-gtnh/deploy/env'
rsync -av -e "$SSH_CMD" "$ENV_FILE" "$PI_HOST:/home/jhein/picoclaw-gtnh/deploy/env/picoclaw.env"
$SSH_CMD "$PI_HOST" 'systemctl --user restart picoclaw-gtnh.service && systemctl --user --no-pager --full status picoclaw-gtnh.service | sed -n "1,80p"'

echo "discord token applied locally and on pi"
