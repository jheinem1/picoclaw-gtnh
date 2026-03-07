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

payload="$(curl -fsS "${BRIDGE_URL}/mc/online?lines=${LINES}")"

printf '%s\n' "$payload" | jq -r --arg lines "$LINES" '
  if .ok != true then
    "error: online lookup failed"
  else
    "Online players (source: " + (.source // "unknown") + "):",
    (if (.players | length) == 0 then
      "(none)"
     else
      (.players[] | "- " + .name)
     end),
    (if (.raw_line // "") != "" then "Raw: " + .raw_line else empty end)
  end
'
