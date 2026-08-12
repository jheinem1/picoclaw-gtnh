#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${DATHOST_ENV_FILE:-$ROOT/deploy/env/dathost-bridge.env}"
ARTIFACT="${1:-$ROOT/build/me-export-mod/greggpt-me-export-1.0.0.jar}"
TARGET_PATH="${ME_EXPORT_TARGET_PATH:-mods/picoclaw-me-export-1.0.0.jar}"
BACKUP_ROOT="${ME_EXPORT_BACKUP_ROOT:-$ROOT/runtime/me-export-backups}"
WARNING_SECONDS="${ME_EXPORT_WARNING_SECONDS:-120}"
START_TIMEOUT_SECONDS="${ME_EXPORT_START_TIMEOUT_SECONDS:-1200}"

if [[ ! -r "$ENV_FILE" ]]; then
  echo "error: missing DatHost environment file: $ENV_FILE" >&2
  exit 1
fi
if [[ ! -s "$ARTIFACT" ]]; then
  echo "error: missing or empty ME export artifact: $ARTIFACT" >&2
  exit 1
fi
if ! [[ "$WARNING_SECONDS" =~ ^[0-9]+$ ]]; then
  echo "error: ME_EXPORT_WARNING_SECONDS must be a non-negative integer" >&2
  exit 1
fi

# This is an operator-owned env file containing shell-style KEY=VALUE entries.
# shellcheck disable=SC1090
source "$ENV_FILE"
: "${DATHOST_API_BASE:?missing DATHOST_API_BASE}"
: "${DATHOST_SERVER_ID:?missing DATHOST_SERVER_ID}"

if [[ -n "${DATHOST_API_TOKEN:-}" ]]; then
  CURL_AUTH=(--user "${DATHOST_API_TOKEN}:")
else
  : "${DATHOST_API_EMAIL:?missing DATHOST_API_EMAIL}"
  : "${DATHOST_API_PASSWORD:?missing DATHOST_API_PASSWORD}"
  CURL_AUTH=(--user "${DATHOST_API_EMAIL}:${DATHOST_API_PASSWORD}")
fi

API_BASE="${DATHOST_API_BASE%/}/game-servers/${DATHOST_SERVER_ID}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="$BACKUP_ROOT/$RUN_ID"
CURRENT_JAR="$BACKUP_DIR/picoclaw-me-export-1.0.0.jar.before"
VERIFY_JAR="$BACKUP_DIR/picoclaw-me-export-1.0.0.jar.uploaded"
ROLLBACK_VERIFY_JAR="$BACKUP_DIR/picoclaw-me-export-1.0.0.jar.restored"
LOG_FILE="$BACKUP_DIR/deploy.log"
mkdir -p "$BACKUP_DIR"
exec > >(tee -a "$LOG_FILE") 2>&1

STOPPED=0
UPLOAD_STARTED=0
START_REQUESTED=0
ROLLBACK_ACTIVE=0

log() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

api() {
  local method="$1"
  local path="$2"
  shift 2
  curl --fail --silent --show-error --retry 3 --retry-all-errors \
    --connect-timeout 15 --max-time 180 \
    "${CURL_AUTH[@]}" -X "$method" "$API_BASE$path" "$@"
}

server_field() {
  local field="$1"
  api GET "" | jq -r ".$field"
}

start_server() {
  log "requesting server start"
  api POST "/start" >/dev/null
  START_REQUESTED=1
}

wait_for_server_stopped() {
  local status_json
  for _ in $(seq 1 60); do
    status_json="$(api GET "")"
    if [[ "$(jq -r '.on' <<<"$status_json")" == false && "$(jq -r '.booting' <<<"$status_json")" == false ]]; then
      STOPPED=1
      return 0
    fi
    sleep 5
  done
  return 1
}

wait_for_server_ready() {
  local status_json server_error
  local attempts=$(( (START_TIMEOUT_SECONDS + 9) / 10 ))
  (( attempts > 0 )) || attempts=1
  for _ in $(seq 1 "$attempts"); do
    status_json="$(api GET "")"
    server_error="$(jq -r '.server_error // ""' <<<"$status_json")"
    if [[ -n "$server_error" ]]; then
      log "error: DatHost reports server_error=$server_error"
      return 1
    fi
    if [[ "$(jq -r '.on' <<<"$status_json")" == true && "$(jq -r '.booting' <<<"$status_json")" == false ]]; then
      STOPPED=0
      return 0
    fi
    sleep 10
  done
  return 1
}

rollback_previous_jar() {
  if [[ "$ROLLBACK_ACTIVE" == 1 ]]; then
    log "error: refusing recursive rollback attempt"
    return 1
  fi
  ROLLBACK_ACTIVE=1

  if [[ ! -s "$CURRENT_JAR" ]]; then
    log "error: cannot roll back because the fresh pre-deploy backup is unavailable"
    return 1
  fi

  if [[ "$START_REQUESTED" == 1 ]]; then
    log "stopping the failed post-deploy server before rollback"
    api POST "/stop" >/dev/null || true
    if ! wait_for_server_stopped; then
      log "error: server did not stop within 300s; refusing to replace a live JAR"
      return 1
    fi
  fi

  log "restoring the freshly backed-up JAR"
  api POST "/files/$TARGET_PATH" \
    -F "file=@$CURRENT_JAR;filename=picoclaw-me-export-1.0.0.jar" >/dev/null
  api POST "/files/sync" >/dev/null
  api GET "/files/$TARGET_PATH" -o "$ROLLBACK_VERIFY_JAR"
  local restored_sha
  restored_sha="$(sha256sum "$ROLLBACK_VERIFY_JAR" | awk '{print $1}')"
  if [[ "$restored_sha" != "$OLD_SHA" ]]; then
    log "error: rollback JAR hash mismatch expected=$OLD_SHA actual=$restored_sha"
    return 1
  fi
  log "rollback JAR verified sha256=$restored_sha"

  START_REQUESTED=0
  start_server
  if ! wait_for_server_ready; then
    log "error: restored server did not become ready within $START_TIMEOUT_SECONDS seconds"
    return 1
  fi
  log "rollback recovery verified: restored server is on and no longer booting"
}

restore_and_start_on_error() {
  local rc=$?
  trap - ERR
  log "deployment failed rc=$rc"
  if [[ "$UPLOAD_STARTED" == 1 ]]; then
    if ! rollback_previous_jar; then
      log "error: automated rollback failed; manual recovery is required"
    fi
  elif [[ "$STOPPED" == 1 && "$START_REQUESTED" == 0 ]]; then
    start_server || true
  fi
  exit "$rc"
}
trap restore_and_start_on_error ERR

NEW_SHA="$(sha256sum "$ARTIFACT" | awk '{print $1}')"
NEW_SIZE="$(stat -c %s "$ARTIFACT")"
log "preflight artifact=$ARTIFACT size=$NEW_SIZE sha256=$NEW_SHA target=$TARGET_PATH"

if [[ "$(server_field on)" != true ]]; then
  log "error: server is not currently on; refusing an unattended deploy"
  exit 1
fi

WARNING_MINUTES="$(( (WARNING_SECONDS + 59) / 60 ))"
WARNING_TEXT="Server restart in ${WARNING_MINUTES} minute(s) for a GregGPT update. Please get somewhere safe."
api POST "/console" --data-urlencode "line=say $WARNING_TEXT" >/dev/null
log "sent in-game warning; waiting ${WARNING_SECONDS}s"
sleep "$WARNING_SECONDS"

log "requesting server stop"
api POST "/stop" >/dev/null
if ! wait_for_server_stopped; then
  log "error: server did not stop within 300s"
  false
fi
log "server is stopped"

api POST "/files/sync" >/dev/null
api GET "/files/$TARGET_PATH" -o "$CURRENT_JAR"
OLD_SHA="$(sha256sum "$CURRENT_JAR" | awk '{print $1}')"
OLD_SIZE="$(stat -c %s "$CURRENT_JAR")"
log "fresh backup saved path=$CURRENT_JAR size=$OLD_SIZE sha256=$OLD_SHA"

UPLOAD_STARTED=1
api POST "/files/$TARGET_PATH" \
  -F "file=@$ARTIFACT;filename=picoclaw-me-export-1.0.0.jar" >/dev/null
api POST "/files/sync" >/dev/null
api GET "/files/$TARGET_PATH" -o "$VERIFY_JAR"
UPLOADED_SHA="$(sha256sum "$VERIFY_JAR" | awk '{print $1}')"
if [[ "$UPLOADED_SHA" != "$NEW_SHA" ]]; then
  log "error: uploaded JAR hash mismatch expected=$NEW_SHA actual=$UPLOADED_SHA"
  false
fi
log "uploaded JAR verified sha256=$UPLOADED_SHA"

START_EPOCH="$(date -u +%s)"
start_server
for _ in $(seq 1 $((START_TIMEOUT_SECONDS / 10))); do
  STATUS_JSON="$(api GET "")"
  ON="$(jq -r '.on' <<<"$STATUS_JSON")"
  BOOTING="$(jq -r '.booting' <<<"$STATUS_JSON")"
  SERVER_ERROR="$(jq -r '.server_error // ""' <<<"$STATUS_JSON")"
  if [[ -n "$SERVER_ERROR" ]]; then
    log "error: DatHost reports server_error=$SERVER_ERROR"
    false
  fi
  if [[ "$ON" == true && "$BOOTING" == false ]]; then
    api POST "/files/sync" >/dev/null || true
    POSITIONS_JSON="$(mktemp)"
    if api GET "/files/world/greggpt/player_positions.json" -o "$POSITIONS_JSON" 2>/dev/null; then
      GENERATED_EPOCH="$(jq -r '(.generated_at | fromdateiso8601) // 0' "$POSITIONS_JSON" 2>/dev/null || printf 0)"
      rm -f "$POSITIONS_JSON"
      if [[ "$GENERATED_EPOCH" =~ ^[0-9]+$ ]] && (( GENERATED_EPOCH >= START_EPOCH )); then
        log "server and new player-position export verified generated_epoch=$GENERATED_EPOCH"
        STOPPED=0
        trap - ERR
        exit 0
      fi
    else
      rm -f "$POSITIONS_JSON"
    fi
  fi
  sleep 10
done

log "error: server did not produce a fresh player_positions.json within ${START_TIMEOUT_SECONDS}s"
false
