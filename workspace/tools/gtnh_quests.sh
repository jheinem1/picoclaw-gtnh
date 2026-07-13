#!/usr/bin/env sh
set -eu

WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INDEX_FILE="${GTNH_QUEST_INDEX_FILE:-$WORKSPACE_DIR/state/quest_index.json}"
STATUS_FILE="${GTNH_QUEST_STATUS_FILE:-$WORKSPACE_DIR/state/quest_status.json}"
REFRESH_FILE="${GTNH_INVENTORY_REFRESH_FILE:-$WORKSPACE_DIR/state/inventory_refresh.json}"
MAX_LIST_LIMIT=500

usage() {
  cat <<'USAGE'
usage:
  sh gtnh_quests status
  sh gtnh_quests open-json [--limit <n>]
  sh gtnh_quests completed-json [--limit <n>]
  sh gtnh_quests show <quest_id>
  sh gtnh_quests refresh
USAGE
  exit 2
}

require_index() {
  [ -f "$INDEX_FILE" ] || {
    echo "quest index not built yet: $INDEX_FILE"
    exit 1
  }
}

normalize_list_limit() {
  value="$1"
  case "$value" in
    ''|*[!0-9]*)
      echo "limit must be a positive integer" >&2
      exit 2
      ;;
  esac
  if [ "$value" -lt 1 ]; then
    echo "limit must be >= 1" >&2
    exit 2
  fi
  if [ "$value" -gt "$MAX_LIST_LIMIT" ]; then
    value="$MAX_LIST_LIMIT"
  fi
  printf '%s\n' "$value"
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
      "Party: " + (.party.name // "(none)") +
        " | Members: " + ((.party.member_count // 0)|tostring) +
        " | Progress files: " + ((.source.matched_progress_files // 0)|tostring) + "/" + ((.source.progress_files // 0)|tostring),
      (if ((.party.members // []) | length) > 0 then
        "Members: " + ((.party.members // []) | map((.name // .uuid) + (if (.progress_file_found // false) then "" else " (no progress file)" end)) | join(", "))
       else empty end),
      "Quests: " + ((.stats.quest_count // 0)|tostring) +
        " | Open: " + ((.stats.open_count // 0)|tostring) +
        " | Completed: " + ((.stats.completed_count // 0)|tostring) +
        " | Required items: " + ((.stats.required_item_count // 0)|tostring),
      "Planning states: Ready: " + ((.stats.ready_count // 0)|tostring) +
        " | In progress: " + ((.stats.in_progress_count // 0)|tostring) +
        " | Locked: " + ((.stats.locked_count // 0)|tostring) +
        " | Claimable: " + ((.stats.claimable_count // 0)|tostring),
      (if (.stale.quests // false) then "WARNING: quest data is stale" else empty end),
      (if ((.warnings // []) | length) > 0 then
        "Warnings:\n" + ((.warnings // []) | map("- " + (. | tostring)) | join("\n"))
       else
        "Warnings: none"
       end),
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
    limit="$(normalize_list_limit "$limit")"
    require_index
    jq --argjson limit "$limit" '
      {
        generated_at,
        source,
        party,
        stats,
        warnings,
        quests: [(.quests // [])[] | select((.completed // false) | not)][: $limit]
      }
    ' "$INDEX_FILE"
    ;;
  completed-json)
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
    limit="$(normalize_list_limit "$limit")"
    require_index
    jq --argjson limit "$limit" '
      {
        generated_at,
        source,
        party,
        stats,
        warnings,
        quests: [(.quests // [])[] | select(.completed // false)][: $limit]
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
  refresh)
    mkdir -p "$(dirname "$REFRESH_FILE")"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '{"requested_at":"%s","scope":"quests","requested_by":"tool"}\n' "$ts" > "$REFRESH_FILE"
    echo "quest refresh requested"
    ;;
  *) usage ;;
esac
