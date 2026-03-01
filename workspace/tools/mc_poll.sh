#!/usr/bin/env sh
set -eu

BRIDGE_URL="${DATHOST_BRIDGE_URL:-http://dathost-bridge:8080}"
DEFAULT_LINES="${DATHOST_CONSOLE_LINES:-500}"
LINES="${1:-$DEFAULT_LINES}"

case "$LINES" in
  ''|*[!0-9]*)
    echo "lines must be numeric" >&2
    exit 2
    ;;
esac

curl -fsS "${BRIDGE_URL}/mc/console?lines=${LINES}"
