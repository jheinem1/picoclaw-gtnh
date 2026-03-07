#!/usr/bin/env sh
set -eu

TASKS_FILE="${GTNH_TASKS_FILE:-state/gtnh_tasks.tsv}"
TASKS_DIR="$(dirname "$TASKS_FILE")"
UPDATED_FILE="${GTNH_TASKS_UPDATED_FILE:-state/gtnh_tasks.updated}"
STATUS_FILE="${GTNH_TASKS_STATUS_FILE:-state/gtnh_task_status_updates.json}"

usage() {
  cat <<'USAGE'
usage:
  sh gtnh_tasks add "<title>" [--priority low|med|high] [--area <name>] [--status todo|doing|paused|done] [--owner <id>] [--paused-reason "<text>"] [--description "<text>"]
  sh gtnh_tasks list [--all|--open|--done] [--area <name>]
  sh gtnh_tasks board
  sh gtnh_tasks board-code
  sh gtnh_tasks board-json
  sh gtnh_tasks in-progress-json
  sh gtnh_tasks move <id> --status todo|doing|paused|done [--owner <id>] [--reason "<text>"]
  sh gtnh_tasks reassign <id> <owner>
  sh gtnh_tasks pause <id> "<reason>"
  sh gtnh_tasks unpause <id>
  sh gtnh_tasks describe <id> "<description>"
  sh gtnh_tasks status-update <id> "<update>"
  sh gtnh_tasks status-history <id>
  sh gtnh_tasks done <id>
  sh gtnh_tasks reopen <id>
  sh gtnh_tasks show <id>
  sh gtnh_tasks summary
USAGE
  exit 2
}

now_utc() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

sanitize() {
  printf '%s' "$1" | tr '\n\r\t' '   ' | sed 's/  */ /g; s/^ //; s/ $//'
}

ensure_store() {
  mkdir -p "$TASKS_DIR"
  if [ ! -f "$TASKS_FILE" ]; then
    printf 'id\tstatus\tpriority\tarea\tcreated_at\tupdated_at\ttitle\tkanban_status\tsort_key\towner\tpaused_reason\tdescription\n' > "$TASKS_FILE"
  fi
}

ensure_status_store() {
  mkdir -p "$(dirname "$STATUS_FILE")"
  if [ ! -f "$STATUS_FILE" ]; then
    printf '{}\n' > "$STATUS_FILE"
  fi
}

migrate_store() {
  cols="$(awk -F '\t' 'NR==1{print NF; exit}' "$TASKS_FILE")"
  [ -n "$cols" ] || cols=0
  if [ "$cols" -eq 12 ]; then
    return
  fi

  tmp="$(mktemp)"
  awk -F '\t' -v OFS='\t' '
    NR==1 {
      print "id","status","priority","area","created_at","updated_at","title","kanban_status","sort_key","owner","paused_reason","description"
      next
    }
    {
      id=$1
      status=$2
      priority=$3
      area=$4
      created_at=$5
      updated_at=$6
      title=$7

      kanban=""
      sort_key=""
      owner=""
      paused_reason=""
      description=""

      if (NF >= 13) {
        kanban=$9
        sort_key=$10
        owner=$11
        paused_reason=$12
        description=$13
      } else if (NF >= 12) {
        kanban=$8
        sort_key=$9
        owner=$10
        paused_reason=$11
        description=$12
      }

      if (kanban=="") {
        if (status=="done") kanban="done"
        else if (status=="doing") kanban="doing"
        else kanban="todo"
      }
      if (sort_key=="" || sort_key !~ /^-?[0-9]+$/) sort_key=id+0

      print id,status,priority,area,created_at,updated_at,title,kanban,sort_key,owner,paused_reason,description
    }
  ' "$TASKS_FILE" > "$tmp"
  mv "$tmp" "$TASKS_FILE"
}

touch_updated() {
  d="$(dirname "$UPDATED_FILE")"
  mkdir -p "$d"
  date -u +"%Y-%m-%dT%H:%M:%SZ" > "$UPDATED_FILE"
}

next_id() {
  awk -F '\t' 'NR==1{next} { if ($1+0>max) max=$1+0 } END{ print max+1 }' "$TASKS_FILE"
}

next_sort_key() {
  awk -F '\t' 'NR==1{next} { if ($9+0>max) max=$9+0 } END{ print max+1 }' "$TASKS_FILE"
}

require_id() {
  id="$1"
  case "$id" in
    ''|*[!0-9]*) echo "error: id must be numeric" >&2; exit 2 ;;
  esac
}

require_kanban_status() {
  s="$1"
  case "$s" in
    todo|doing|paused|done) ;;
    *) echo "error: status must be todo, doing, paused, or done" >&2; exit 2 ;;
  esac
}

row_exists() {
  id="$1"
  awk -F '\t' -v id="$id" 'NR>1 && $1==id { found=1 } END{ exit(found?0:1) }' "$TASKS_FILE"
}

row_owner() {
  id="$1"
  awk -F '\t' -v id="$id" 'NR>1 && $1==id { print $10; exit }' "$TASKS_FILE"
}

row_kanban_status() {
  id="$1"
  awk -F '\t' -v id="$id" '
    NR>1 && $1==id {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      print k
      exit
    }
  ' "$TASKS_FILE"
}

update_row_timestamp() {
  id="$1"
  ts="$2"
  tmp="$(mktemp)"
  awk -F '\t' -v OFS='\t' -v id="$id" -v ts="$ts" '
    NR==1 { print; next }
    $1==id { $6=ts; print; next }
    { print }
  ' "$TASKS_FILE" > "$tmp"
  mv "$tmp" "$TASKS_FILE"
}

cmd_add() {
  [ "$#" -ge 1 ] || usage
  title="$(sanitize "$1")"
  shift
  [ -n "$title" ] || { echo "error: title cannot be empty" >&2; exit 2; }

  priority="med"
  area="general"
  kanban_status="todo"
  owner=""
  paused_reason=""
  description=""

  while [ "$#" -gt 0 ]; do
    case "${1:-}" in
      --priority)
        [ "$#" -ge 2 ] || usage
        priority="$2"
        shift 2
        ;;
      --area)
        [ "$#" -ge 2 ] || usage
        area="$(sanitize "$2")"
        shift 2
        ;;
      --status)
        [ "$#" -ge 2 ] || usage
        kanban_status="$2"
        shift 2
        ;;
      --owner)
        [ "$#" -ge 2 ] || usage
        owner="$(sanitize "$2")"
        shift 2
        ;;
      --paused-reason)
        [ "$#" -ge 2 ] || usage
        paused_reason="$(sanitize "$2")"
        shift 2
        ;;
      --description)
        [ "$#" -ge 2 ] || usage
        description="$(sanitize "$2")"
        shift 2
        ;;
      *)
        usage
        ;;
    esac
  done

  case "$priority" in
    low|med|high) ;;
    *) echo "error: priority must be low, med, or high" >&2; exit 2 ;;
  esac
  require_kanban_status "$kanban_status"
  if [ "$kanban_status" = "doing" ] && [ -z "$owner" ]; then
    echo "error: owner is required when status is doing (use --owner <id>)" >&2
    exit 2
  fi

  if [ "$kanban_status" = "paused" ] && [ -z "$paused_reason" ]; then
    paused_reason="blocked"
  fi

  [ -n "$area" ] || area="general"
  id="$(next_id)"
  ts="$(now_utc)"
  sort_key="$(next_sort_key)"

  status="open"
  if [ "$kanban_status" = "done" ]; then
    status="done"
  fi

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$id" "$status" "$priority" "$area" "$ts" "$ts" "$title" "$kanban_status" "$sort_key" "$owner" "$paused_reason" "$description" >> "$TASKS_FILE"
  touch_updated
  echo "added task #$id"
}

cmd_list() {
  status_filter="open"
  area_filter=""

  while [ "$#" -gt 0 ]; do
    case "${1:-}" in
      --open) status_filter="open"; shift ;;
      --done) status_filter="done"; shift ;;
      --all) status_filter="all"; shift ;;
      --area)
        [ "$#" -ge 2 ] || usage
        area_filter="$(sanitize "$2")"
        shift 2
        ;;
      *)
        usage
        ;;
    esac
  done

  awk -F '\t' -v sf="$status_filter" -v af="$area_filter" '
    BEGIN {
      shown = 0
      printf "ID  KANBAN  PRI  AREA      TITLE\n"
      printf "--  ------  ---  --------  ------------------------------\n"
    }
    NR == 1 { next }
    {
      k = $8
      if (k == "") {
        if ($2 == "done") k = "done"
        else if ($2 == "doing") k = "doing"
        else k = "todo"
      }
      if (sf == "open" && k == "done") next
      if (sf == "done" && k != "done") next
      if (af != "" && $4 != af) next
      printf "%-3s %-7s %-4s %-9s %s\n", $1, k, $3, $4, $7
      shown++
    }
    END {
      if (shown == 0) print "(no matching tasks)"
    }
  ' "$TASKS_FILE"
}

cmd_mark() {
  id="$1"
  new_kanban="$2"
  new_reason="${3:-}"
  new_owner="${4:-}"
  require_id "$id"
  require_kanban_status "$new_kanban"
  row_exists "$id" || { echo "error: task #$id not found" >&2; exit 1; }

  if [ "$new_kanban" = "doing" ]; then
    effective_owner="$new_owner"
    if [ -z "$effective_owner" ]; then
      effective_owner="$(row_owner "$id" | tr -d '\r\n')"
    fi
    if [ -z "$effective_owner" ]; then
      echo "error: owner is required when moving task #$id to doing (use --owner <id>)" >&2
      exit 2
    fi
  fi

  ts="$(now_utc)"
  tmp="$(mktemp)"
  awk -F '\t' -v OFS='\t' -v id="$id" -v k="$new_kanban" -v r="$new_reason" -v o="$new_owner" -v ts="$ts" '
    NR==1 { print; next }
    $1==id {
      $8=k
      if (k=="done") $2="done"
      else $2="open"
      if (o!="") $10=o
      if (k=="paused") {
        if (r!="") $11=r
        else if ($11=="") $11="blocked"
      } else {
        $11=""
      }
      $6=ts
      print
      next
    }
    { print }
  ' "$TASKS_FILE" > "$tmp"
  mv "$tmp" "$TASKS_FILE"
  touch_updated
  echo "$new_kanban task #$id"
}

cmd_move() {
  [ "$#" -ge 3 ] || usage
  id="$1"
  shift
  [ "$1" = "--status" ] || usage
  shift
  new_status="$1"
  shift
  owner=""
  reason=""
  while [ "$#" -gt 0 ]; do
    case "${1:-}" in
      --owner)
        [ "$#" -ge 2 ] || usage
        owner="$(sanitize "$2")"
        shift 2
        ;;
      --reason)
        [ "$#" -ge 2 ] || usage
        reason="$(sanitize "$2")"
        shift 2
        ;;
      *)
        usage
        ;;
    esac
  done
  cmd_mark "$id" "$new_status" "$reason" "$owner"
}

cmd_pause() {
  [ "$#" -eq 2 ] || usage
  id="$1"
  reason="$(sanitize "$2")"
  [ -n "$reason" ] || { echo "error: pause reason cannot be empty" >&2; exit 2; }
  cmd_mark "$id" "paused" "$reason"
}

cmd_unpause() {
  [ "$#" -eq 1 ] || usage
  id="$1"
  cmd_mark "$id" "todo"
}

cmd_reassign() {
  [ "$#" -eq 2 ] || usage
  id="$1"
  owner="$(sanitize "$2")"
  require_id "$id"
  [ -n "$owner" ] || { echo "error: owner cannot be empty" >&2; exit 2; }
  row_exists "$id" || { echo "error: task #$id not found" >&2; exit 1; }

  kstatus="$(row_kanban_status "$id" | tr -d '\r\n')"
  if [ "$kstatus" != "doing" ]; then
    echo "error: task #$id is not in doing; only in-progress tasks can be reassigned" >&2
    exit 2
  fi

  ts="$(now_utc)"
  tmp="$(mktemp)"
  awk -F '\t' -v OFS='\t' -v id="$id" -v o="$owner" -v ts="$ts" '
    NR==1 { print; next }
    $1==id {
      $10=o
      $6=ts
      print
      next
    }
    { print }
  ' "$TASKS_FILE" > "$tmp"
  mv "$tmp" "$TASKS_FILE"
  touch_updated
  echo "reassigned task #$id to $owner"
}

cmd_describe() {
  [ "$#" -eq 2 ] || usage
  id="$1"
  description="$(sanitize "$2")"
  require_id "$id"
  row_exists "$id" || { echo "error: task #$id not found" >&2; exit 1; }

  ts="$(now_utc)"
  tmp="$(mktemp)"
  awk -F '\t' -v OFS='\t' -v id="$id" -v d="$description" -v ts="$ts" '
    NR==1 { print; next }
    $1==id {
      $12 = d
      $6 = ts
      print
      next
    }
    { print }
  ' "$TASKS_FILE" > "$tmp"
  mv "$tmp" "$TASKS_FILE"
  touch_updated
  echo "updated description for task #$id"
}

cmd_status_update() {
  [ "$#" -eq 2 ] || usage
  id="$1"
  text="$(sanitize "$2")"
  require_id "$id"
  [ -n "$text" ] || { echo "error: status update cannot be empty" >&2; exit 2; }
  row_exists "$id" || { echo "error: task #$id not found" >&2; exit 1; }

  ensure_status_store
  ts="$(now_utc)"
  author="$(sanitize "${GTNH_TASKS_STATUS_AUTHOR:-}")"
  python3 - "$STATUS_FILE" "$id" "$ts" "$author" "$text" <<'PY'
import json
import os
import sys
import tempfile

path, task_id, timestamp, author, text = sys.argv[1:]
try:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
except FileNotFoundError:
    data = {}
except json.JSONDecodeError:
    data = {}

updates = data.get(task_id, [])
if not isinstance(updates, list):
    updates = []
updates.append({"timestamp": timestamp, "author": author, "text": text})
data[task_id] = updates

directory = os.path.dirname(path) or "."
fd, tmp = tempfile.mkstemp(dir=directory)
os.close(fd)
with open(tmp, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
os.replace(tmp, path)
PY
  update_row_timestamp "$id" "$ts"
  touch_updated
  echo "added status update for task #$id"
}

cmd_status_history() {
  [ "$#" -eq 1 ] || usage
  id="$1"
  require_id "$id"
  row_exists "$id" || { echo "error: task #$id not found" >&2; exit 1; }
  ensure_status_store

  python3 - "$STATUS_FILE" "$id" <<'PY'
import json
import sys

path, task_id = sys.argv[1:]
try:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
except FileNotFoundError:
    data = {}
except json.JSONDecodeError:
    data = {}

updates = data.get(task_id, [])
print(f"task_id: {task_id}")
if not updates:
    print("(no status updates)")
    raise SystemExit(0)

for entry in updates:
    ts = entry.get("timestamp", "")
    author = entry.get("author", "").strip()
    text = entry.get("text", "").strip()
    if author:
        print(f"- {ts} [{author}] {text}")
    else:
        print(f"- {ts} {text}")
PY
}

cmd_show() {
  [ "$#" -eq 1 ] || usage
  id="$1"
  require_id "$id"
  awk -F '\t' -v id="$id" '
    NR==1 { next }
    $1==id {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      print "id:            " $1
      print "status:        " $2
      print "kanban_status: " k
      print "priority:      " $3
      print "area:          " $4
      print "created_at:    " $5
      print "updated_at:    " $6
      print "title:         " $7
      print "sort_key:      " $9
      print "owner:         " $10
      print "paused_reason: " $11
      print "description:   " $12
      found=1
    }
    END {
      if (!found) {
        print "error: task #" id " not found" > "/dev/stderr"
        exit 1
      }
    }
  ' "$TASKS_FILE"
}

cmd_summary() {
  awk -F '\t' '
    NR==1 { next }
    {
      total++
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      if (k=="todo") todo++
      if (k=="doing") doing++
      if (k=="paused") paused++
      if (k=="done") done++
      if ($3=="high" && k!="done") high_open++
    }
    END {
      printf "total: %d\n", total
      printf "todo: %d\n", todo
      printf "doing: %d\n", doing
      printf "paused: %d\n", paused
      printf "done: %d\n", done
      printf "high_open: %d\n", high_open
    }
  ' "$TASKS_FILE"
}

cmd_board() {
  awk -F '\t' '
    BEGIN {
      todo_total=0
      doing_total=0
      paused_total=0
      done_total=0
      high_open=0
      med_open=0
      low_open=0
      todo_shown=0
      doing_shown=0
      paused_shown=0
      done_shown=0
    }
    NR==1 { next }
    {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }

      if (k!="done") {
        if ($3=="high") high_open++
        if ($3=="med") med_open++
        if ($3=="low") low_open++
      }

      line=sprintf("#%s [%s/%s] %s", $1, $3, $4, $7)

      if (k=="todo") {
        todo_total++
        if (todo_shown < 12) {
          todo_shown++
          todo_items[todo_shown]=line
        }
        next
      }

      if (k=="doing") {
        doing_total++
        owner=$10
        if (owner=="") owner="unassigned"
        line=sprintf("#%s [%s/%s] %s (in progress: %s)", $1, $3, $4, $7, owner)
        if (doing_shown < 12) {
          doing_shown++
          doing_items[doing_shown]=line
        }
        next
      }

      if (k=="paused") {
        paused_total++
        paused_reason=$11
        if (paused_reason=="") paused_reason="blocked"
        line=sprintf("#%s [%s/%s] %s (paused: %s)", $1, $3, $4, $7, paused_reason)
        if (paused_shown < 12) {
          paused_shown++
          paused_items[paused_shown]=line
        }
        next
      }

      if (k=="done") {
        done_total++
        done_shown++
        done_items[done_shown]=line
        if (done_shown > 8) {
          for (i=1; i<8; i++) done_items[i]=done_items[i+1]
          done_items[8]=line
          done_shown=8
        }
      }
    }
    END {
      print "GTNH Kanban Board"
      print "Open: " (todo_total+doing_total+paused_total) " (high " high_open ", med " med_open ", low " low_open ")"
      print ""
      print "Todo:"
      if (todo_shown == 0) print "- (none)"
      for (i=1; i<=todo_shown; i++) print "- " todo_items[i]
      if (todo_total > todo_shown) print "- ... +" (todo_total-todo_shown) " more todo tasks"
      print ""
      print "Doing:"
      if (doing_shown == 0) print "- (none)"
      for (i=1; i<=doing_shown; i++) print "- " doing_items[i]
      if (doing_total > doing_shown) print "- ... +" (doing_total-doing_shown) " more doing tasks"
      print ""
      print "Paused:"
      if (paused_shown == 0) print "- (none)"
      for (i=1; i<=paused_shown; i++) print "- " paused_items[i]
      if (paused_total > paused_shown) print "- ... +" (paused_total-paused_shown) " more paused tasks"
      print ""
      print "Done:"
      if (done_shown == 0) print "- (none)"
      for (i=1; i<=done_shown; i++) print "- " done_items[i]
    }
  ' "$TASKS_FILE"
}

cmd_board_code() {
  printf '```text\n'
  cmd_board
  printf '\n```\n'
}

cmd_board_json() {
  awk -F '\t' '
    function esc(s) {
      gsub(/\\/,"\\\\",s)
      gsub(/"/,"\\\"",s)
      gsub(/\r/," ",s)
      gsub(/\n/," ",s)
      return s
    }
    NR==1 { next }
    {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      sk=$9
      if (sk=="" || sk !~ /^-?[0-9]+$/) sk=0

      item=sprintf("{\"id\":%d,\"status\":\"%s\",\"priority\":\"%s\",\"area\":\"%s\",\"created_at\":\"%s\",\"updated_at\":\"%s\",\"title\":\"%s\",\"sort_key\":%d,\"owner\":\"%s\",\"paused_reason\":\"%s\",\"description\":\"%s\"}", $1+0, esc(k), esc($3), esc($4), esc($5), esc($6), esc($7), sk+0, esc($10), esc($11), esc($12))
      total++

      if (k=="doing") {
        doing++
        if (doing_json!="") doing_json=doing_json ","
        doing_json=doing_json item
      } else if (k=="paused") {
        paused++
        if (paused_json!="") paused_json=paused_json ","
        paused_json=paused_json item
      } else if (k=="done") {
        done++
        if (done_json!="") done_json=done_json ","
        done_json=done_json item
      } else {
        todo++
        if (todo_json!="") todo_json=todo_json ","
        todo_json=todo_json item
      }
    }
    END {
      printf("{\"board\":\"GTNH Kanban\",\"summary\":{\"total\":%d,\"todo\":%d,\"doing\":%d,\"paused\":%d,\"done\":%d},\"columns\":{\"todo\":[%s],\"doing\":[%s],\"paused\":[%s],\"done\":[%s]}}\n", total+0, todo+0, doing+0, paused+0, done+0, todo_json, doing_json, paused_json, done_json)
    }
  ' "$TASKS_FILE"
}

cmd_in_progress_json() {
  ensure_status_store
  python3 - "$TASKS_FILE" "$STATUS_FILE" <<'PY'
import csv
import json
import sys

tasks_path, status_path = sys.argv[1:]

try:
    with open(status_path, "r", encoding="utf-8") as fh:
        status_updates = json.load(fh)
except FileNotFoundError:
    status_updates = {}
except json.JSONDecodeError:
    status_updates = {}

tasks = []
with open(tasks_path, "r", encoding="utf-8", newline="") as fh:
    reader = csv.DictReader(fh, delimiter="\t")
    for row in reader:
        if not row:
            continue
        kanban = (row.get("kanban_status") or "").strip()
        status = (row.get("status") or "").strip()
        if not kanban:
            if status == "done":
                kanban = "done"
            elif status == "doing":
                kanban = "doing"
            else:
                kanban = "todo"
        if kanban != "doing":
            continue

        task_id = (row.get("id") or "").strip()
        try:
            numeric_id = int(task_id)
        except ValueError:
            continue
        try:
            sort_key = int((row.get("sort_key") or "0").strip())
        except ValueError:
            sort_key = 0

        updates = status_updates.get(task_id, [])
        if not isinstance(updates, list):
            updates = []

        tasks.append({
            "id": numeric_id,
            "status": kanban,
            "priority": (row.get("priority") or "").strip(),
            "area": (row.get("area") or "").strip(),
            "created_at": (row.get("created_at") or "").strip(),
            "updated_at": (row.get("updated_at") or "").strip(),
            "title": (row.get("title") or "").strip(),
            "sort_key": sort_key,
            "owner": (row.get("owner") or "").strip(),
            "paused_reason": (row.get("paused_reason") or "").strip(),
            "description": (row.get("description") or "").strip(),
            "status_updates": [
                {
                    "timestamp": str(entry.get("timestamp", "")).strip(),
                    "author": str(entry.get("author", "")).strip(),
                    "text": str(entry.get("text", "")).strip(),
                }
                for entry in updates if isinstance(entry, dict)
            ],
        })

tasks.sort(key=lambda item: (item["sort_key"], item["id"]))
print(json.dumps({"tasks": tasks, "count": len(tasks)}, separators=(",", ":")))
PY
}

ensure_store
migrate_store
ensure_status_store

cmd="${1:-}"
[ -n "$cmd" ] || usage
shift || true

case "$cmd" in
  add) cmd_add "$@" ;;
  list) cmd_list "$@" ;;
  board) [ "$#" -eq 0 ] || usage; cmd_board ;;
  board-code) [ "$#" -eq 0 ] || usage; cmd_board_code ;;
  board-json) [ "$#" -eq 0 ] || usage; cmd_board_json ;;
  in-progress-json) [ "$#" -eq 0 ] || usage; cmd_in_progress_json ;;
  move) cmd_move "$@" ;;
  reassign) cmd_reassign "$@" ;;
  pause) cmd_pause "$@" ;;
  unpause) cmd_unpause "$@" ;;
  describe) cmd_describe "$@" ;;
  status-update) cmd_status_update "$@" ;;
  status-history) cmd_status_history "$@" ;;
  done) [ "$#" -eq 1 ] || usage; cmd_mark "$1" "done" ;;
  reopen) [ "$#" -eq 1 ] || usage; cmd_mark "$1" "todo" ;;
  show) cmd_show "$@" ;;
  summary) [ "$#" -eq 0 ] || usage; cmd_summary ;;
  *) usage ;;
esac
