#!/usr/bin/env sh
set -eu

WORKDIR="${QUESTBOOK_WORKDIR:-/root/.greggpt/workspace}"
DATA_FILE="${QUESTBOOK_DATA_FILE:-questbooks/atmons-0.14.1-beta.json}"
STATE_FILE="${QUESTBOOK_STATE_FILE:-state/atmons-questbook-state.json}"
SYNC_STATE_FILE="${QUESTBOOK_SYNC_STATE_FILE:-state/atmons-questbook-sync.json}"
CHANNEL_ID="${QUESTBOOK_CHANNEL_ID:-}"
INTERVAL="${QUESTBOOK_SYNC_INTERVAL_SECONDS:-60}"
ENABLED="${QUESTBOOK_SYNC_ENABLED:-false}"

cd "$WORKDIR"

is_true() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

while :; do
  if ! is_true "$ENABLED"; then
    echo "questbook-sync disabled"
    sleep 300
    continue
  fi
  if [ -z "$CHANNEL_ID" ]; then
    echo "questbook-sync missing QUESTBOOK_CHANNEL_ID"
    sleep 300
    continue
  fi

  python3 tools/questbook_tracker.py sync-channel \
    --data-file "$DATA_FILE" \
    --state-file "$STATE_FILE" \
    --sync-state-file "$SYNC_STATE_FILE" \
    --channel-id "$CHANNEL_ID" || true
  sleep "$INTERVAL"
done
