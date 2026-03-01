#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -t -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" '
set -euo pipefail
cd /home/jhein/picoclaw-gtnh/deploy
/usr/bin/podman-compose -f compose.yaml run --rm picoclaw-gateway auth login --provider openai --device-code
'
