#!/usr/bin/env sh
set -eu

WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
export GTNH_WORKSPACE="${GTNH_WORKSPACE:-$WORKSPACE_DIR}"

if [ -n "${GTNH_QUEST_QUERY_BIN:-}" ] && [ -x "$GTNH_QUEST_QUERY_BIN" ]; then
  exec "$GTNH_QUEST_QUERY_BIN" "$@"
fi
if [ -x "$WORKSPACE_DIR/tools/gtnh_quest_query" ]; then
  exec "$WORKSPACE_DIR/tools/gtnh_quest_query" "$@"
fi
if command -v gtnh_quest_query >/dev/null 2>&1; then
  exec gtnh_quest_query "$@"
fi

REPO_DIR="$(cd "$WORKSPACE_DIR/.." && pwd)"
if command -v go >/dev/null 2>&1 && [ -f "$REPO_DIR/go.mod" ] && [ -d "$REPO_DIR/quest-query" ]; then
  exec go run "$REPO_DIR/quest-query" "$@"
fi

echo "error: gtnh_quest_query is not installed" >&2
exit 1
