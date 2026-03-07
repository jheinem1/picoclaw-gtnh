#!/usr/bin/env sh
set -eu

TASKS_FILE="${GTNH_TASKS_FILE:-state/gtnh_tasks.tsv}"
STATE_FILE="${GTNH_TASK_CHECKIN_STATE_FILE:-state/gtnh_task_checkin_state.tsv}"
PENDING_FILE="${GTNH_TASK_CHECKIN_PENDING_FILE:-state/gtnh_task_checkin_pending_ids.txt}"
INTERVAL_SECONDS="${GTNH_TASK_CHECKIN_INTERVAL_SECONDS:-21600}"
MENTION_ID="${GTNH_TASK_CHECKIN_MENTION_ID:-862546744453103636}"
MAX_ITEMS="${GTNH_TASK_CHECKIN_MAX_ITEMS:-3}"

now_epoch() {
  date +%s
}

ensure_state_dir() {
  mkdir -p "$(dirname "$STATE_FILE")"
  mkdir -p "$(dirname "$PENDING_FILE")"
}

ensure_state_file() {
  ensure_state_dir
  if [ ! -f "$STATE_FILE" ]; then
    printf 'id\tlast_sent_epoch\n' > "$STATE_FILE"
  fi
}

count_doing() {
  [ -f "$TASKS_FILE" ] || { echo 0; return; }
  awk -F '\t' '
    NR==1 { next }
    {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      if (k=="doing") c++
    }
    END { print c+0 }
  ' "$TASKS_FILE"
}

due_items() {
  now="$1"
  [ -f "$TASKS_FILE" ] || return 0
  ensure_state_file

  awk -F '\t' -v OFS='\t' -v now="$now" -v interval="$INTERVAL_SECONDS" '
    FNR==NR {
      if (NR>1 && $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/) sent[$1]=$2
      next
    }
    NR==1 { next }
    {
      k=$8
      if (k=="") {
        if ($2=="done") k="done"
        else if ($2=="doing") k="doing"
        else k="todo"
      }
      if (k!="doing") next
      id=$1
      last=(id in sent)?sent[id]:0
      if ((now-last) < interval) next
      printf "%s\t%s\t%s\t%s\n", id, $3, $4, $7
    }
  ' "$STATE_FILE" "$TASKS_FILE" | sort -t "$(printf '\t')" -k1,1n | head -n "$MAX_ITEMS"
}

emit_skip() {
  reason="$1"
  printf 'ACTION=SKIP\n'
  printf 'REASON=%s\n' "$reason"
}

emit_send() {
  msg="$1"
  printf 'ACTION=SEND\n'
  printf 'MESSAGE=%s\n' "$msg"
}

write_pending_ids() {
  ensure_state_dir
  ids="$1"
  printf '%s\n' "$ids" | tr ',' '\n' | awk 'NF>0' > "$PENDING_FILE"
}

mark_sent_ids() {
  ensure_state_file
  now="$(now_epoch)"
  tmp="$(mktemp)"

  awk -F '\t' -v OFS='\t' -v now="$now" '
    NR==1 { print; next }
    { print }
  ' "$STATE_FILE" > "$tmp"

  for id in "$@"; do
    case "$id" in
      ''|*[!0-9]*) continue ;;
    esac

    if awk -F '\t' -v id="$id" 'NR>1 && $1==id {found=1} END {exit(found?0:1)}' "$tmp"; then
      tmp2="$(mktemp)"
      awk -F '\t' -v OFS='\t' -v id="$id" -v now="$now" '
        NR==1 { print; next }
        $1==id { $2=now; print; next }
        { print }
      ' "$tmp" > "$tmp2"
      mv "$tmp2" "$tmp"
    else
      printf '%s\t%s\n' "$id" "$now" >> "$tmp"
    fi
  done

  mv "$tmp" "$STATE_FILE"
}

cmd="${1:-check}"
case "$cmd" in
  check)
    doing_count="$(count_doing)"
    if [ "$doing_count" -le 0 ]; then
      emit_skip "no-doing-tasks"
      exit 0
    fi

    now="$(now_epoch)"
    due="$(due_items "$now" || true)"
    if [ -z "$due" ]; then
      emit_skip "not-due"
      exit 0
    fi

    ids="$(printf '%s\n' "$due" | awk -F '\t' 'NF>0{print $1}' | paste -sd',' -)"
    write_pending_ids "$ids"

    items="$(printf '%s\n' "$due" | awk -F '\t' 'NF>0{printf "#%s [%s/%s] %s;", $1,$2,$3,$4}' | sed 's/;*$//')"
    msg="In-progress task check-in: <@${MENTION_ID}> can you post updates for: ${items}"
    emit_send "$msg"
    ;;
  mark-sent)
    if [ "$#" -gt 1 ]; then
      shift
      mark_sent_ids "$@"
      printf 'OK=marked\n'
      exit 0
    fi

    if [ ! -f "$PENDING_FILE" ]; then
      printf 'OK=no-pending\n'
      exit 0
    fi

    pending_ids="$(awk 'NF>0{print $1}' "$PENDING_FILE" | paste -sd' ' - || true)"
    if [ -z "$pending_ids" ]; then
      printf 'OK=no-pending\n'
      exit 0
    fi

    # shellcheck disable=SC2086
    mark_sent_ids $pending_ids
    : > "$PENDING_FILE"
    printf 'OK=marked\n'
    ;;
  *)
    echo "usage: sh gtnh_task_checkin [check|mark-sent [id...]]" >&2
    exit 2
    ;;
esac
