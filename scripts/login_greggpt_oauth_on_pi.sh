#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_DIR="${PI_DIR:-/home/jhein/greggpt-gtnh}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
CODEX_BIN="${CODEX_BIN:-codex}"
SSH_KEY_FILE="$(mktemp)"
AUTH_TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -f "$SSH_KEY_FILE"
  rm -rf "$AUTH_TMP_DIR"
}
trap cleanup EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"
SSH_CMD="ssh -o IdentitiesOnly=yes -o IdentityAgent=$HOME/.1password/agent.sock -i $SSH_KEY_FILE"

command -v "$CODEX_BIN" >/dev/null 2>&1 || {
  echo "error: Codex CLI is required on the workstation; install or set CODEX_BIN" >&2
  exit 1
}

echo "Starting an isolated Codex device-code login on the workstation."
echo "The resulting credentials will be transferred atomically to the Pi."
CODEX_HOME="$AUTH_TMP_DIR" "$CODEX_BIN" login --device-auth

LOCAL_AUTH_FILE="$AUTH_TMP_DIR/auth.json"
if ! jq -e '
  .auth_mode == "chatgpt" and
  (.tokens.access_token | type == "string" and length > 0) and
  (.tokens.refresh_token | type == "string" and length > 0)
' "$LOCAL_AUTH_FILE" >/dev/null; then
  echo "error: Codex login did not produce a compatible ChatGPT OAuth auth.json" >&2
  exit 1
fi
chmod 0600 "$LOCAL_AUTH_FILE"

REMOTE_AUTH_DIR="$PI_DIR/runtime/greggpt"
REMOTE_AUTH_FILE="$REMOTE_AUTH_DIR/auth.json"
REMOTE_UPLOAD="$REMOTE_AUTH_DIR/.auth-upload-$(date -u +%Y%m%dT%H%M%SZ)-$$"
$SSH_CMD "$PI_HOST" "mkdir -p '$REMOTE_AUTH_DIR' && chmod 0700 '$REMOTE_AUTH_DIR'"
rsync -av -e "$SSH_CMD" "$LOCAL_AUTH_FILE" "$PI_HOST:$REMOTE_UPLOAD"

$SSH_CMD "$PI_HOST" "
set -euo pipefail
touch '$REMOTE_AUTH_FILE.lock'
chmod 0600 '$REMOTE_AUTH_FILE.lock'
exec 9>'$REMOTE_AUTH_FILE.lock'
flock -w 60 9
jq -e '.auth_mode == \"chatgpt\" and (.tokens.access_token | length > 0) and (.tokens.refresh_token | length > 0)' '$REMOTE_UPLOAD' >/dev/null
install -m 0600 '$REMOTE_UPLOAD' '$REMOTE_AUTH_FILE.next'
mv -f '$REMOTE_AUTH_FILE.next' '$REMOTE_AUTH_FILE'
rm -f '$REMOTE_UPLOAD'
"

echo "Updated Pi OAuth credentials at $REMOTE_AUTH_FILE."
