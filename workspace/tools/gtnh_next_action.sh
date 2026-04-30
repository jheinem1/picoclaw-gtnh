#!/usr/bin/env sh
set -eu

WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
export GTNH_WORKSPACE="${GTNH_WORKSPACE:-$WORKSPACE_DIR}"
exec python3 "$WORKSPACE_DIR/tools/gtnh_next_action.py" "$@"
