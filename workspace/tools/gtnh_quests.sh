#!/usr/bin/env sh
set -eu

WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INDEX_FILE="${GTNH_QUEST_INDEX_FILE:-$WORKSPACE_DIR/state/quest_index.json}"
STATUS_FILE="${GTNH_QUEST_STATUS_FILE:-$WORKSPACE_DIR/state/quest_status.json}"

usage() {
  cat <<'USAGE'
usage:
  sh gtnh_quests status
  sh gtnh_quests open-json [--limit <n>]
  sh gtnh_quests show <quest_id>
USAGE
  exit 2
}

require_index() {
  [ -f "$INDEX_FILE" ] || {
    echo "quest index not built yet: $INDEX_FILE"
    exit 1
  }
}

case "${1:-}" in
  status)
    if [ ! -f "$STATUS_FILE" ]; then
      echo "quest status not found yet: $STATUS_FILE"
      echo "Enable inventory-sync quest indexing or wait for the next sync cycle."
      exit 1
    fi
    jq -r '
      "Quest Index Status",
      "Generated: " + (.generated_at // "(unknown)"),
      "Quest scan: " + (.source.quests_scan_at // "(never)"),
      "DatHost sync: " + (.source.dathost_sync_at // "(unknown)"),
      "Quests: " + ((.stats.quest_count // 0)|tostring) +
        " | Open: " + ((.stats.open_count // 0)|tostring) +
        " | Completed: " + ((.stats.completed_count // 0)|tostring) +
        " | Required items: " + ((.stats.required_item_count // 0)|tostring),
      (if (.stale.quests // false) then "WARNING: quest data is stale" else empty end),
      (if ((.errors // {}) | length) > 0 then
        "Errors:\n" + ((.errors | to_entries | map("- " + .key + ": " + (.value|tostring)) | join("\n")) )
       else
        "Errors: none"
       end)
    ' "$STATUS_FILE"
    ;;
  open-json)
    shift
    limit=50
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --limit)
          [ "$#" -ge 2 ] || usage
          limit="$2"
          shift 2
          ;;
        *) usage ;;
      esac
    done
    require_index
    jq --argjson limit "$limit" '
      {
        generated_at,
        source,
        stats,
        quests: [(.quests // [])[] | select((.completed // false) | not)][: $limit]
      }
    ' "$INDEX_FILE"
    ;;
  show)
    [ "$#" -eq 2 ] || usage
    id="$2"
    require_index
    jq --arg id "$id" '
      (.quests // [])[] | select((.id|tostring) == $id)
    ' "$INDEX_FILE"
    ;;
  *) usage ;;
esac
