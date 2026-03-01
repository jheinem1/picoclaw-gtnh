#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" '
set -euo pipefail
mkdir -p /home/jhein/.config/systemd/user
cat > /home/jhein/.config/systemd/user/picoclaw-gtnh.service <<"UNIT"
[Unit]
Description=PicoClaw GTNH bot stack (Podman Compose)
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=%h/picoclaw-gtnh/deploy
ExecStart=/usr/bin/podman-compose --env-file %h/picoclaw-gtnh/deploy/env/picoclaw.env -f %h/picoclaw-gtnh/deploy/compose.yaml up -d --remove-orphans
ExecStop=/usr/bin/podman-compose --env-file %h/picoclaw-gtnh/deploy/env/picoclaw.env -f %h/picoclaw-gtnh/deploy/compose.yaml down
TimeoutStartSec=180
TimeoutStopSec=60

[Install]
WantedBy=default.target
UNIT

systemctl --user daemon-reload
systemctl --user enable picoclaw-gtnh.service
'
