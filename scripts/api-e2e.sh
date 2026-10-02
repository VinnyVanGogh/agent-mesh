#!/usr/bin/env bash
# api-e2e.sh: run the StayPoint API Postman suite headless (STA-346).
#
# Default: builds cmd/staypoint-apitest-server, starts it on a fresh SQLite
# file in a temp dir with HOME pointed at that temp dir, starts a Paperclip
# stub for the /api/fleet/* proxies, runs tests/api/staypoint.postman_collection.json
# with newman, then checks the SSE stream with curl. The real StayPoint DB
# (~/.staypoint/staypoint.db) and the live Paperclip API are never touched.
#
# Usage:
#   scripts/api-e2e.sh                     # throwaway daemon (normal use)
#   scripts/api-e2e.sh --target URL        # an already-running THROWAWAY daemon;
#                                          # needs STAYPOINT_API_TOKEN, refuses :41421
#   scripts/api-e2e.sh --folder "01 Tasks" # run selected folders (repeatable)
#   scripts/api-e2e.sh --serve             # start the throwaway daemon + stub and
#                                          # print baseUrl/token for Postman; Ctrl-C stops
#
# Env:
#   STAYPOINT_API_TOKEN  token for the daemon (generated when starting one)
#   E2E_JUNIT=path       also write a JUnit report
#   E2E_KEEP_TMP=1       keep the temp dir (DB, daemon log) for debugging
#
# Exit status is non-zero if newman reports any failure or the SSE check fails.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_DIR="$ROOT/tests/api"
COLLECTION="$API_DIR/staypoint.postman_collection.json"
ENVIRONMENT="$API_DIR/staypoint.postman_environment.json"
SSE_FOLDER_PREFIX="13 SSE"

TARGET=""
SERVE=0
FOLDERS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --target) TARGET="${2:?--target needs a URL}"; shift 2 ;;
    --folder) FOLDERS+=("${2:?--folder needs a name}"); shift 2 ;;
    --serve) SERVE=1; shift ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for bin in jq python3 curl; do
  command -v "$bin" >/dev/null || { echo "api-e2e: $bin is required" >&2; exit 2; }
done

TMP="$(mktemp -d "${TMPDIR:-/tmp}/staypoint-api-e2e.XXXXXX")"
PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  if [ "${E2E_KEEP_TMP:-0}" = "1" ]; then
    echo "api-e2e: kept $TMP"
  else
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT INT TERM

# The committed collection must match its generator.
python3 "$API_DIR/build_collection.py" "$TMP/collection.json" >/dev/null
if ! diff -q "$TMP/collection.json" "$COLLECTION" >/dev/null; then
  echo "api-e2e: $COLLECTION is stale; run: python3 tests/api/build_collection.py" >&2
  exit 1
fi

# Every route registered in internal/server needs an authenticated request.
if ! python3 "$API_DIR/build_collection.py" --check-routes "$ROOT"; then
  echo "api-e2e: add the routes above to tests/api/build_collection.py" >&2
  exit 1
fi

wait_for() { # wait_for <seconds> <command...>
  local deadline=$(( $(date +%s) + $1 )); shift
  until "$@"; do
    [ "$(date +%s)" -ge "$deadline" ] && return 1
    sleep 0.2
  done
}

if [ -n "$TARGET" ]; then
  BASE_URL="${TARGET%/}"
  case "$BASE_URL" in
    *:41421|*:41421/*)
      echo "api-e2e: refusing $BASE_URL: 41421 is the real staypointd port" >&2; exit 2 ;;
  esac
  : "${STAYPOINT_API_TOKEN:?--target needs STAYPOINT_API_TOKEN}"
  TOKEN="$STAYPOINT_API_TOKEN"
else
  command -v go >/dev/null || { echo "api-e2e: go is required" >&2; exit 2; }
  TOKEN="${STAYPOINT_API_TOKEN:-$(python3 -c 'import secrets; print(secrets.token_hex(32))')}"
  mkdir -p "$TMP/home"

  python3 "$API_DIR/paperclip_stub.py" "$TMP/stub.port" &
  PIDS+=("$!")
  wait_for 10 test -s "$TMP/stub.port" || { echo "api-e2e: paperclip stub did not start" >&2; exit 1; }
  STUB_URL="http://127.0.0.1:$(cat "$TMP/stub.port")"

  echo "api-e2e: building staypoint-apitest-server"
  (cd "$ROOT" && go build -o "$TMP/staypoint-apitest-server" ./cmd/staypoint-apitest-server)

  # env -u: the daemon's fleet proxies attach PAPERCLIP_API_KEY to every call.
  env -u PAPERCLIP_API_KEY -u PAPERCLIP_COMPANY_ID -u PAPERCLIP_API_URL -u XDG_RUNTIME_DIR \
    HOME="$TMP/home" STAYPOINT_API_TOKEN="$TOKEN" \
    "$TMP/staypoint-apitest-server" --db "$TMP/staypoint.db" --paperclip-url "$STUB_URL" \
    >"$TMP/daemon.out" 2>"$TMP/daemon.err" &
  PIDS+=("$!")
  if ! wait_for 30 grep -q '^READY ' "$TMP/daemon.out"; then
    echo "api-e2e: daemon did not start" >&2
    cat "$TMP/daemon.err" >&2
    exit 1
  fi
  BASE_URL="$(sed -n 's/^READY //p' "$TMP/daemon.out")"
  echo "api-e2e: throwaway daemon at $BASE_URL (db: $TMP/staypoint.db)"
fi

if [ "$SERVE" = 1 ]; then
  [ -n "$TARGET" ] && { echo "api-e2e: --serve and --target are exclusive" >&2; exit 2; }
  cat <<EOS
api-e2e: throwaway daemon ready for Postman
  baseUrl = $BASE_URL
  token   = $TOKEN
Import tests/api/staypoint.postman_collection.json and
tests/api/staypoint.postman_environment.json, paste the two values above into
the environment, then run the folders in order. Ctrl-C stops the daemon and
deletes its temp DB.
EOS
  wait
  exit 0
fi

if command -v newman >/dev/null; then
  NEWMAN=(newman)
else
  command -v npx >/dev/null || { echo "api-e2e: install newman (npm i -g newman) or node/npx" >&2; exit 2; }
  NEWMAN=(npx --yes newman@6)
fi

if [ ${#FOLDERS[@]} -eq 0 ]; then
  while IFS= read -r name; do
    FOLDERS+=("$name")
  done < <(jq -r --arg sse "$SSE_FOLDER_PREFIX" '.item[].name | select(startswith($sse) | not)' "$COLLECTION")
fi
FOLDER_ARGS=()
for f in "${FOLDERS[@]}"; do FOLDER_ARGS+=(--folder "$f"); done

REPORTERS=(--reporters cli)
if [ -n "${E2E_JUNIT:-}" ]; then
  REPORTERS=(--reporters cli,junit --reporter-junit-export "$E2E_JUNIT")
fi

status=0
"${NEWMAN[@]}" run "$COLLECTION" -e "$ENVIRONMENT" \
  --env-var "baseUrl=$BASE_URL" --env-var "token=$TOKEN" \
  "${FOLDER_ARGS[@]}" "${REPORTERS[@]}" \
  --timeout-request 30000 --color off || status=$?

# SSE never ends, so newman cannot assert it. Check both endpoints for
# (a) replay of buffered events after a cursor and (b) live delivery of an
# event published while the client is connected.
sse_ok=1
for path in /api/events /api/sse; do
  out="$TMP/sse$(echo "$path" | tr / _)"
  curl -sN -m 3 -H "Authorization: Bearer $TOKEN" "$BASE_URL$path?cursor=1" >"$out.replay" || true
  if grep -q '^event: ' "$out.replay" && grep -q '^id: ' "$out.replay"; then
    echo "api-e2e: SSE $path replay after cursor: ok"
  else
    echo "api-e2e: SSE $path FAILED: nothing replayed after cursor=1" >&2
    sse_ok=0
  fi

  curl -sN -m 4 -H "Authorization: Bearer $TOKEN" "$BASE_URL$path" >"$out.live" &
  live_pid=$!
  sleep 1
  marker="sse-live-$RANDOM"
  curl -s -o /dev/null -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$marker\"}" "$BASE_URL/api/tasks"
  wait "$live_pid" || true
  if grep -q '^event: task_created' "$out.live" && grep -q "$marker" "$out.live"; then
    echo "api-e2e: SSE $path live task_created delivered: ok"
  else
    echo "api-e2e: SSE $path FAILED: live task_created for $marker not received" >&2
    sse_ok=0
  fi
done
code="$(curl -s -o /dev/null -m 3 -w '%{http_code}' "$BASE_URL/api/events" || true)"
if [ "$code" = "401" ]; then
  echo "api-e2e: SSE without token -> 401: ok"
else
  echo "api-e2e: SSE without token returned $code, want 401" >&2
  sse_ok=0
fi
[ "$sse_ok" = 1 ] || status=1

if [ "$status" -ne 0 ]; then
  echo "api-e2e: FAILED" >&2
  [ -z "$TARGET" ] && echo "api-e2e: daemon stderr tail:" >&2 && tail -20 "$TMP/daemon.err" >&2
  exit "$status"
fi
echo "api-e2e: PASSED"
