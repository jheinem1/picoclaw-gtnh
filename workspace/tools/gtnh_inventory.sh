#!/usr/bin/env sh
set -eu

WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INDEX_FILE="${GTNH_INVENTORY_INDEX_FILE:-$WORKSPACE_DIR/state/inventory_index.json}"
STATUS_FILE="${GTNH_INVENTORY_STATUS_FILE:-$WORKSPACE_DIR/state/inventory_status.json}"
REFRESH_FILE="${GTNH_INVENTORY_REFRESH_FILE:-$WORKSPACE_DIR/state/inventory_refresh.json}"
DEFAULT_LIMIT="${INVENTORY_DEFAULT_LIMIT:-20}"
MAX_RESULTS="${INVENTORY_MAX_RESULTS:-100}"
ITEMS_INDEX_FILE="${GTNH_ITEMS_INDEX:-$WORKSPACE_DIR/gtnh-data/index/item_index.tsv}"
usage() {

  cat <<'USAGE'
usage:
  sh gtnh_inventory status
  sh gtnh_inventory find [--item <mod:name[:damage]> [--any-damage] | --id <num> --damage <num>] [--player <name|uuid>] [--scope players|chests|both] [--limit <n>]
  sh gtnh_inventory find-item --query "<name>" [--scope players|chests|both] [--limit <n>]
  sh gtnh_inventory player --name <player> | --uuid <uuid> [--all]
  sh gtnh_inventory chest --x <int> --y <int> --z <int> [--dim 0|-1|1]
  sh gtnh_inventory refresh [--players|--chests|--all]
USAGE
  exit 2
}

require_file() {
  path="$1"
  [ -f "$path" ] || {
    echo "inventory index not built yet: $path"
    exit 1
  }
}

is_int() {
  case "$1" in
    ''|*[!0-9-]*) return 1 ;;
    -) return 1 ;;
    *) return 0 ;;
  esac
}

cap_limit() {
  n="$1"
  if ! is_int "$n"; then
    echo "$DEFAULT_LIMIT"
    return
  fi
  if [ "$n" -lt 1 ]; then
    n=1
  fi
  if [ "$n" -gt "$MAX_RESULTS" ]; then
    n="$MAX_RESULTS"
  fi
  echo "$n"
}

lookup_item_label() {
  id="$1"
  damage="$2"
  [ -f "$ITEMS_INDEX_FILE" ] || { echo "unknown"; return; }
  awk -F '\t' -v want_id="$id" -v want_damage="$damage" '
    NR == 1 { next }
    {
      slug = $1
      item_id = slug
      sub(/d.*/, "", item_id)
      item_damage = 0
      if (slug ~ /d[0-9-]+$/) {
        item_damage = slug
        sub(/^.*d/, "", item_damage)
      }
      if (item_id == want_id && item_damage == want_damage) {
        if ($2 != "") {
          v = $2
          gsub(/[[:space:]]+/, " ", v)
          sub(/^ /, "", v); sub(/ $/, "", v)
          print v
          exit
        }
        if ($3 != "") {
          v = $3
          gsub(/[[:space:]]+/, " ", v)
          sub(/^ /, "", v); sub(/ $/, "", v)
          print v
          exit
        }
      }
      if (item_id == want_id && fallback == "") {
        if ($2 != "") { fallback = $2 }
        else if ($3 != "") { fallback = $3 }
      }
    }
    END {
      if (fallback != "") {
        gsub(/[[:space:]]+/, " ", fallback)
        sub(/^ /, "", fallback); sub(/ $/, "", fallback)
        print fallback
      }
    }
  ' "$ITEMS_INDEX_FILE"
}

format_stack_lines_with_names() {
  while IFS= read -r line; do
    case "$line" in
      STACK\|*)
        rec="${line#STACK|}"
        src="${rec%%|*}"
        rec="${rec#*|}"
        id="${rec%%|*}"
        rec="${rec#*|}"
        damage="${rec%%|*}"
        rec="${rec#*|}"
        count="${rec%%|*}"
        rec="${rec#*|}"
        slot="${rec%%|*}"
        if [ "${rec#*|}" != "$rec" ]; then
          custom="${rec#*|}"
        else
          custom=""
        fi
        custom="$(printf '%s' "$custom" | tr '\r\n\t' '   ' | tr -s ' ' | sed 's/^ //; s/ $//')"
        name="$(lookup_item_label "$id" "$damage" | tr '\r\n\t' '   ' | tr -s ' ' | sed 's/^ //; s/ $//')"
        [ -n "$name" ] || name="unknown"
        source_note=""
        case "$src" in
          *:nested) source_note=" src=nested" ;;
        esac
        if [ -n "$custom" ]; then
          echo "- $id:$damage ($name | custom: $custom) x$count slot=$slot$source_note"
        else
          echo "- $id:$damage ($name) x$count slot=$slot$source_note"
        fi
        ;;
      *)
        echo "$line"
        ;;
    esac
  done
}

print_inventory_process_hint() {
  cat >&2 <<'EOF'
next-step:
- run exactly one inventory command (no cd/&& chaining):
  sh gtnh_inventory find --item <mod:name[:damage]> --scope both
  OR
  sh gtnh_inventory find-item --query "<name>" --scope both
- if query is ambiguous, pick one exact mod:name[:damage] and rerun with --item.
EOF
}

cmd_status() {
  if [ ! -f "$STATUS_FILE" ]; then
    echo "inventory status not found yet: $STATUS_FILE"
    echo "Run refresh after inventory-sync is enabled."
    exit 1
  fi

  jq -r '
    "Inventory Index Status",
    "Generated: " + (.generated_at // "(unknown)"),
    "Players scan: " + (.source.players_scan_at // "(never)"),
    "Chests scan: " + (.source.chests_scan_at // "(never)"),
    "DatHost sync: " + (.source.dathost_sync_at // "(unknown)"),
    "Players: " + ((.stats.player_count // 0)|tostring) +
      " | Containers: " + ((.stats.chest_count // 0)|tostring) +
      " | Item keys: " + ((.stats.indexed_item_keys // 0)|tostring),
    (if (.stale.players // false) then "WARNING: players data is stale (>30m)" else empty end),
    (if (.stale.chests // false) then "WARNING: chests data is stale (>24h)" else empty end),
    (if ((.errors // {}) | length) > 0 then
      "Errors:\n" + ((.errors | to_entries | map("- " + .key + ": " + (.value|tostring)) | join("\n")) )
     else
      "Errors: none"
     end)
  ' "$STATUS_FILE"
}

cmd_find() {
  id=""
  item=""
  damage=""
  any_damage="0"
  scope="both"
  limit="$DEFAULT_LIMIT"
  player_filter=""
  resolved_mode="exact"
  item_base=""

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --id)
        [ "$#" -ge 2 ] || usage
        id="$2"
        shift 2
        ;;
      --item)
        [ "$#" -ge 2 ] || usage
        item="$2"
        shift 2
        ;;
      --damage)
        [ "$#" -ge 2 ] || usage
        damage="$2"
        shift 2
        ;;
      --any-damage)
        any_damage="1"
        shift
        ;;
      --scope)
        [ "$#" -ge 2 ] || usage
        scope="$2"
        shift 2
        ;;
      --player)
        [ "$#" -ge 2 ] || usage
        player_filter="$2"
        shift 2
        ;;
      --limit)
        [ "$#" -ge 2 ] || usage
        limit="$2"
        shift 2
        ;;
      *) usage ;;
    esac
  done

  case "$scope" in
    players|chests|both) ;;
    *) echo "error: --scope must be players, chests, or both" >&2; exit 2 ;;
  esac

  [ -n "$id$item" ] || { echo "error: provide --item <mod:name[:damage]> or --id with --damage" >&2; exit 2; }
  [ -z "$id" ] || [ -z "$item" ] || { echo "error: use either --item or --id/--damage, not both" >&2; exit 2; }

  keys=""

  if [ -n "$item" ]; then
    # Allowed formats: modid:name or modid:name:damage
    printf '%s' "$item" | grep -Eq '^[A-Za-z0-9_.-]+:[A-Za-z0-9_.-]+(:[0-9-]+)?$' || {
      echo "error: --item must be modname:name or modname:name:damage" >&2
      exit 2
    }

    item_base="$item"
    item_damage=""
    if printf '%s' "$item" | grep -Eq '^[A-Za-z0-9_.-]+:[A-Za-z0-9_.-]+:[0-9-]+$'; then
      item_base="${item%:*}"
      item_damage="${item##*:}"
    fi

    if [ -n "$damage" ] && [ -n "$item_damage" ] && [ "$damage" != "$item_damage" ]; then
      echo "error: --damage conflicts with --item damage suffix" >&2
      exit 2
    fi
    if [ -n "$item_damage" ]; then
      damage="$item_damage"
    fi

    if [ "$any_damage" = "1" ] && [ -n "$damage" ]; then
      echo "error: --any-damage cannot be combined with an explicit damage" >&2
      exit 2
    fi

    [ -f "$ITEMS_INDEX_FILE" ] || {
      echo "error: item index not found: $ITEMS_INDEX_FILE" >&2
      exit 1
    }

    filter_damage="$damage"
    if [ "$any_damage" = "1" ]; then
      filter_damage=""
    fi

    keys="$(awk -F '	' -v want="$item_base" -v dmg="$filter_damage" '
      NR == 1 { next }
      $3 == want {
        slug = $1
        item_id = slug
        sub(/d.*/, "", item_id)
        item_damage = 0
        if (slug ~ /d[0-9-]+$/) {
          item_damage = slug
          sub(/^.*d/, "", item_damage)
        }
        if (dmg == "" || item_damage == dmg) {
          printf "%s:%s\n", item_id, item_damage
        }
      }
    ' "$ITEMS_INDEX_FILE" | sort -u)"

    key_count="$(printf '%s\n' "$keys" | sed '/^$/d' | wc -l | tr -d ' ')"
    [ "$key_count" -gt 0 ] || {
      echo "error: no items matched --item $item_base${filter_damage:+ with damage $filter_damage}" >&2
      print_inventory_process_hint
      exit 1
    }

    if [ -z "$damage" ] || [ "$any_damage" = "1" ]; then
      resolved_mode="any"
      echo "Resolved item '$item' -> any damage across $key_count metas"
    else
      key="$(printf '%s\n' "$keys" | sed '/^$/d' | head -n1)"
      id="${key%%:*}"
      damage="${key##*:}"
      keys="$key"
      resolved_mode="exact"
      echo "Resolved item '$item' -> id=$id damage=$damage"
    fi
  fi

  if [ -z "$keys" ]; then
    [ -n "$id" ] || { echo "error: missing --id after resolution" >&2; exit 2; }
    is_int "$id" || { echo "error: --id must be numeric" >&2; exit 2; }

    # Strict mode: prevent incorrect numeric-only calls like --id 11305.
    [ -n "$damage" ] || {
      echo "error: --id requires --damage; use --item modname:name[:damage] (use sh gtnh_find_item '<query>' to pick one) for PicoClaw requests" >&2
      print_inventory_process_hint
      exit 2
    }
    is_int "$damage" || { echo "error: --damage must be numeric" >&2; exit 2; }
    keys="$id:$damage"
    resolved_mode="exact"
  fi

  keys_json="$(printf '%s\n' "$keys" | sed '/^$/d' | jq -R . | jq -s .)"

  limit="$(cap_limit "$limit")"
  require_file "$INDEX_FILE"

  if [ "$resolved_mode" = "exact" ]; then
    key_first="$(printf '%s\n' "$keys" | sed '/^$/d' | head -n1)"
    item_id="${key_first%%:*}"
    item_damage="${key_first##*:}"
    item_label="$(lookup_item_label "$item_id" "$item_damage" | tr '\r\n\t' '   ' | tr -s ' ' | sed 's/^ //; s/ $//')"
    if [ -n "$item_label" ]; then
      echo "Item: $item_id:$item_damage ($item_label)"
    else
      echo "Item: $item_id:$item_damage"
    fi
  elif [ -n "$item_base" ]; then
    echo "Item: $item_base (any damage)"
  fi

  jq -r --argjson keys "$keys_json" --arg scope "$scope" --argjson limit "$limit" --arg mode "$resolved_mode" --arg player "$player_filter" '
    def merge_players(arr):
      reduce arr[] as $p ({};
        .[$p.uuid] = (
          if has($p.uuid) then
            .[$p.uuid] + {total_count: (.[$p.uuid].total_count + ($p.total_count // 0)), locations: ((.[$p.uuid].locations // []) + ($p.locations // []))}
          else
            $p
          end
        )
      ) | to_entries | map(.value) | sort_by(-(.total_count // 0), (.name // ""));

    def merge_chests(arr):
      reduce arr[] as $c ({};
        .[(($c.dim|tostring)+":"+($c.x|tostring)+":"+($c.y|tostring)+":"+($c.z|tostring))] = (
          if has((($c.dim|tostring)+":"+($c.x|tostring)+":"+($c.y|tostring)+":"+($c.z|tostring))) then
            .[(($c.dim|tostring)+":"+($c.x|tostring)+":"+($c.y|tostring)+":"+($c.z|tostring))] + {total_count: (.[(($c.dim|tostring)+":"+($c.x|tostring)+":"+($c.y|tostring)+":"+($c.z|tostring))].total_count + ($c.total_count // 0))}
          else
            $c
          end
        )
      ) | to_entries | map(.value) | sort_by(-(.total_count // 0), .dim, .x, .y, .z);

    . as $root
    | (reduce ($keys[]) as $k ({players:[], chests:[]};
        .players += (($root.item_index[$k].players // []))
        | .chests += (($root.item_index[$k].chests // []))
      )) as $hits
    | "Inventory find mode=" + $mode + " keys=" + (($keys|length)|tostring) + " scope=" + $scope + (if ($player|length)>0 then " player=" + $player else "" end),
      (if $scope == "players" or $scope == "both" then
         "Players:",
         ((merge_players($hits.players // [])
            | map(select(($player|length)==0 or (((.name // "")|ascii_downcase)==($player|ascii_downcase) or ((.uuid // "")|ascii_downcase)==($player|ascii_downcase))))
            | .[:$limit]) |
            if length == 0 then "(none)"
            else .[] | "- " + (.name // .uuid // "unknown") + " (" + (.uuid // "?") + ") count=" + ((.total_count // 0)|tostring) +
               " pos=(" + ((.pos.x // 0)|tostring) + "," + ((.pos.y // 0)|tostring) + "," + ((.pos.z // 0)|tostring) + ") dim=" + ((.dim // 0)|tostring) +
               (([ (.locations[]?.custom_name // empty) | tostring | gsub("[\\r\\n\\t]"; " ") | gsub("^ +| +$"; "") | select(length>0) ] | unique) as $cn
                  | if ($cn|length) > 0 then
                      " custom=" + (($cn[:3]) | join(" / "))
                    else
                      ""
                    end)
            end)
       else empty end),
      (if $scope == "chests" or $scope == "both" then
         "Containers:",
         ((merge_chests($hits.chests // []))[:$limit] |
            if length == 0 then "(none)"
            else .[] | "- count=" + ((.total_count // 0)|tostring) + " at (" + ((.x // 0)|tostring) + "," + ((.y // 0)|tostring) + "," + ((.z // 0)|tostring) + ") dim=" + ((.dim // 0)|tostring) + " type=" + (.type // "Chest")
            end)
       else empty end)
  ' "$INDEX_FILE"
}
cmd_find_item() {
  query=""
  scope="both"
  limit="$DEFAULT_LIMIT"
  player_filter=""

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --query)
        [ "$#" -ge 2 ] || usage
        query="$2"
        shift 2
        ;;
      --scope)
        [ "$#" -ge 2 ] || usage
        scope="$2"
        shift 2
        ;;
      --player)
        [ "$#" -ge 2 ] || usage
        player_filter="$2"
        shift 2
        ;;
      --limit)
        [ "$#" -ge 2 ] || usage
        limit="$2"
        shift 2
        ;;
      *) usage ;;
    esac
  done

  [ -n "$query" ] || { echo "error: --query is required" >&2; exit 2; }
  case "$scope" in
    players|chests|both) ;;
    *) echo "error: --scope must be players, chests, or both" >&2; exit 2 ;;
  esac
  limit="$(cap_limit "$limit")"

  resolved_json="$(sh "$WORKSPACE_DIR/gtnh_find_item" "$query")"

  candidate_count="$(printf '%s' "$resolved_json" | jq -r '
    if (.ok != true) then 0 else ((.items // []) | length) end
  ')"

  if [ "$candidate_count" -gt 1 ]; then
    echo "error: ambiguous item query '$query' matched $candidate_count items; use --item modname:name[:damage]" >&2
    printf '%s' "$resolved_json" | jq -r '
      (.items // [])
      | .[:8]
      | .[]
      | "- " + (.reg_name // "?") + " (slug=" + (.slug // "?") + ")"
    ' >&2
    print_inventory_process_hint
    exit 2
  fi

  slug="$(printf '%s' "$resolved_json" | jq -r '
    if (.ok != true) then empty
    else ((.items // [])[0].slug // empty)
    end
  ')"

  [ -n "$slug" ] || { echo "item resolve failed for query: $query" >&2; exit 1; }

  id="${slug%%d*}"
  damage=""
  if [ "${slug#*d}" != "$slug" ]; then
    damage="${slug#*d}"
  fi

  is_int "$id" || { echo "resolved item id is not numeric for slug: $slug" >&2; exit 1; }
  if [ -n "$damage" ]; then
    is_int "$damage" || { echo "resolved item damage is not numeric for slug: $slug" >&2; exit 1; }
  fi

  echo "Resolved item query '$query' -> slug=$slug id=$id${damage:+ damage=$damage}"
  if [ -z "$damage" ]; then
    damage="0"
  fi
  cmd_find --id "$id" --damage "$damage" --player "$player_filter" --scope "$scope" --limit "$limit"
}

cmd_player() {
  name=""
  uuid=""
  all="0"

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --name)
        [ "$#" -ge 2 ] || usage
        name="$2"
        shift 2
        ;;
      --uuid)
        [ "$#" -ge 2 ] || usage
        uuid="$2"
        shift 2
        ;;
      --all)
        all="1"
        shift
        ;;
      *) usage ;;
    esac
  done

  [ -n "$name$uuid" ] || { echo "error: provide --name or --uuid" >&2; exit 2; }
  require_file "$INDEX_FILE"

  max_entries="12"
  if [ "$all" = "1" ]; then
    max_entries="10000"
  fi

  jq -r --arg name "$name" --arg uuid "$uuid" --argjson max_entries "$max_entries" '
    .players as $players
    | ($players | map(select((($uuid|length)>0 and (.uuid == $uuid)) or (($name|length)>0 and ((.name|ascii_downcase) == ($name|ascii_downcase))))) | .[0]) as $p
    | if $p == null then
        "player not found"
      else
        "Player: " + ($p.name // $p.uuid),
        "UUID: " + ($p.uuid // "?"),
        "Position: (" + (($p.pos.x // 0)|tostring) + "," + (($p.pos.y // 0)|tostring) + "," + (($p.pos.z // 0)|tostring) + ") dim=" + (($p.dim // 0)|tostring),
        "Inventory stacks: " + (($p.inventory|length)|tostring) + " | Ender stacks: " + (($p.ender|length)|tostring),
        "Top inventory entries:",
        ((($p.inventory // []) | sort_by(-.count) | .[:$max_entries]) |
          if length == 0 then "(none)"
          else .[] | "STACK|" + ((.source // "inv")|tostring|gsub("\\|"; "/")) + "|" + (.id|tostring) + "|" + ((.damage // 0)|tostring) + "|" + ((.count // 0)|tostring) + "|" + ((.slot // 0)|tostring) + "|" + ((.custom_name // "")|tostring|gsub("\\|"; "/"))
          end),
        "Top ender entries:",
        ((($p.ender // []) | sort_by(-.count) | .[:$max_entries]) |
          if length == 0 then "(none)"
          else .[] | "STACK|" + ((.source // "ender")|tostring|gsub("\\|"; "/")) + "|" + (.id|tostring) + "|" + ((.damage // 0)|tostring) + "|" + ((.count // 0)|tostring) + "|" + ((.slot // 0)|tostring) + "|" + ((.custom_name // "")|tostring|gsub("\\|"; "/"))
          end)
      end
  ' "$INDEX_FILE" | format_stack_lines_with_names
}

cmd_chest() {
  x=""
  y=""
  z=""
  dim="0"

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --x)
        [ "$#" -ge 2 ] || usage
        x="$2"
        shift 2
        ;;
      --y)
        [ "$#" -ge 2 ] || usage
        y="$2"
        shift 2
        ;;
      --z)
        [ "$#" -ge 2 ] || usage
        z="$2"
        shift 2
        ;;
      --dim)
        [ "$#" -ge 2 ] || usage
        dim="$2"
        shift 2
        ;;
      *) usage ;;
    esac
  done

  [ -n "$x" ] && [ -n "$y" ] && [ -n "$z" ] || { echo "error: --x --y --z are required" >&2; exit 2; }
  is_int "$x" || { echo "error: --x must be integer" >&2; exit 2; }
  is_int "$y" || { echo "error: --y must be integer" >&2; exit 2; }
  is_int "$z" || { echo "error: --z must be integer" >&2; exit 2; }
  is_int "$dim" || { echo "error: --dim must be integer" >&2; exit 2; }

  require_file "$INDEX_FILE"

  jq -r --argjson x "$x" --argjson y "$y" --argjson z "$z" --argjson dim "$dim" '
    (.chests | map(select(.x == $x and .y == $y and .z == $z and .dim == $dim)) | .[0]) as $c
    | if $c == null then
        "chest not found"
      else
        "Chest at (" + ($x|tostring) + "," + ($y|tostring) + "," + ($z|tostring) + ") dim=" + ($dim|tostring),
        "Type: " + ($c.type // "Chest"),
        "Items:",
        ((($c.items // []) | sort_by(-.count, .id, .damage) ) |
          if length == 0 then "(none)"
          else .[] | "STACK|" + ((.source // "chest")|tostring|gsub("\\|"; "/")) + "|" + (.id|tostring) + "|" + ((.damage // 0)|tostring) + "|" + ((.count // 0)|tostring) + "|" + ((.slot // 0)|tostring) + "|" + ((.custom_name // "")|tostring|gsub("\\|"; "/"))
          end)
      end
  ' "$INDEX_FILE" | format_stack_lines_with_names
}

cmd_refresh() {
  scope="all"
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --players) scope="players"; shift ;;
      --chests) scope="chests"; shift ;;
      --all) scope="all"; shift ;;
      *) usage ;;
    esac
  done

  mkdir -p "$(dirname "$REFRESH_FILE")"
  ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  printf '{"requested_at":"%s","scope":"%s","requested_by":"tool"}\n' "$ts" "$scope" > "$REFRESH_FILE"
  echo "refresh requested ($scope)"
}

cmd="${1:-}"
[ -n "$cmd" ] || usage
shift || true

case "$cmd" in
  status) cmd_status "$@" ;;
  find) cmd_find "$@" ;;
  find-item) cmd_find_item "$@" ;;
  player) cmd_player "$@" ;;
  chest) cmd_chest "$@" ;;
  refresh) cmd_refresh "$@" ;;
  *) usage ;;
esac
