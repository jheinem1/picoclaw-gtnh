#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@192.168.1.59}"
PI_DIR="${PI_DIR:-/home/jhein/picoclaw-gtnh}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
PICOCLAW_REF="${PICOCLAW_REF:-main}"

SSH_KEY_FILE="$(mktemp)"
BUILD_DIR="$(mktemp -d)"
trap 'rm -f "$SSH_KEY_FILE"; rm -rf "$BUILD_DIR"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

SSH_OPTS=(-o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE")

echo "building picoclaw ($PICOCLAW_REF) arm64 binary locally..."
git clone --depth 1 --branch "$PICOCLAW_REF" https://github.com/sipeed/picoclaw.git "$BUILD_DIR/src"
cp -r "$BUILD_DIR/src/workspace" "$BUILD_DIR/src/cmd/picoclaw/internal/onboard/"
(
  cd "$BUILD_DIR/src"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "$BUILD_DIR/picoclaw.custom" ./cmd/picoclaw
)

echo "uploading hotfix binary to pi..."
ssh "${SSH_OPTS[@]}" "$PI_HOST" "mkdir -p '$PI_DIR/runtime/picoclaw'"
scp "${SSH_OPTS[@]}" "$BUILD_DIR/picoclaw.custom" "$PI_HOST:$PI_DIR/runtime/picoclaw/picoclaw.custom"
ssh "${SSH_OPTS[@]}" "$PI_HOST" "chmod +x '$PI_DIR/runtime/picoclaw/picoclaw.custom'"

echo "syncing compose changes and restarting service..."
"$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy_to_pi.sh"
ssh "${SSH_OPTS[@]}" "$PI_HOST" "systemctl --user restart picoclaw-gtnh && sleep 2 && podman exec picoclaw-gateway sh -lc 'picoclaw version'"

echo "hotfix installed"
