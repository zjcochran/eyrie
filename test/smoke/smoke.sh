#!/usr/bin/env bash
# Smoke test of the real image on a real Docker engine: Eyrie on SQLite and
# on PostgreSQL. Needs Docker with Compose and curl. Removes everything it
# created.
#
#   test/smoke/smoke.sh
#
# In GitLab CI with Docker-in-Docker, published ports live on the "docker"
# service host: SMOKE_HOST=docker SMOKE_BIND=0.0.0.0 test/smoke/smoke.sh
# SMOKE_VERSION sets the version built in; SMOKE_JUNIT=/abs/path.xml also
# writes each step as a JUnit test case.
set -euo pipefail
cd "$(dirname "$0")"

export SMOKE_BIND=${SMOKE_BIND:-127.0.0.1}
export SMOKE_VERSION=${SMOKE_VERSION:-smoke}
HOST=${SMOKE_HOST:-127.0.0.1}
# JUnit report: one test case per step, timed from the previous step.
CASES="" COUNT=0 FAILURES=0 LAST=$(date +%s)
xml() { tr -d '\000-\010\013\014\016-\037' | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g'; }
record() { # <step> [failure details]
  local now name; now=$(date +%s); name=$(printf %s "$1" | xml)
  COUNT=$((COUNT + 1))
  CASES+="<testcase classname=\"smoke\" name=\"$name\" time=\"$((now - LAST))\">"
  if [ $# -gt 1 ]; then
    FAILURES=$((FAILURES + 1))
    CASES+="<failure message=\"$name\">$(printf %s "$2" | xml)</failure>"
  fi
  CASES+="</testcase>"
  LAST=$now
}
write_junit() {
  [ -n "${SMOKE_JUNIT:-}" ] || return 0
  printf '<?xml version="1.0" encoding="UTF-8"?>\n<testsuites><testsuite name="smoke" tests="%d" failures="%d">%s</testsuite></testsuites>\n' \
    "$COUNT" "$FAILURES" "$CASES" >"$SMOKE_JUNIT"
}

pass() { record "$*"; printf '\033[32mok\033[0m   %s\n' "$*"; }
fail() {
  local logs; logs=$(docker compose logs --tail 60 sqlite postgres 2>&1 || true)
  record "$*" "$logs"
  printf '\033[31mFAIL\033[0m %s\n%s\n' "$*" "$logs"
  exit 1
}

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ] && [ "$FAILURES" -eq 0 ]; then
    record "smoke test" "aborted with exit status $status"
  fi
  write_junit
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

# wait_for <seconds> <description> <command...>
wait_for() {
  local secs=$1 what=$2; shift 2
  for _ in $(seq "$secs"); do
    if "$@" >/dev/null 2>&1; then pass "$what"; return; fi
    sleep 1
  done
  fail "$what (gave up after ${secs}s)"
}

healthy() { [ "$(docker inspect -f '{{.State.Health.Status}}' "eyriesmoke-$1-1" 2>/dev/null)" = healthy ]; }
both_healthy() { healthy sqlite && healthy postgres; }

# check <service> <port>: what every instance must do.
check() {
  local svc=$1 url=http://$HOST:$2 headers page asset
  wait_for 90 "$svc: /healthz answers ok" sh -c "curl -fsS '$url/healthz' | grep -qx ok"

  curl -fsS "$url/api/v1/info" | grep -qF "\"version\":\"$SMOKE_VERSION\"" ||
    fail "$svc: /api/v1/info does not report version $SMOKE_VERSION"
  pass "$svc: /api/v1/info reports $SMOKE_VERSION"

  headers=$(curl -fsS -D - -o /dev/null "$url/")
  grep -qi "^content-security-policy: default-src 'self'" <<<"$headers" || fail "$svc: no Content-Security-Policy"
  page=$(curl -fsS "$url/monitors")
  grep -qF '<div id="app">' <<<"$page" || fail "$svc: an app route does not serve the app"
  asset=$(grep -oE '/assets/[^"]+\.js' <<<"$page" | head -1)
  [ -n "$asset" ] || fail "$svc: the page links no script"
  curl -fsS -D - -o /dev/null "$url$asset" | grep -qi '^cache-control: .*immutable' ||
    fail "$svc: $asset is missing or not cached"
  pass "$svc: the embedded web app is served"

  [ "$(docker inspect -f '{{.Config.User}}' "eyriesmoke-$svc-1")" = 65532:65532 ] || fail "$svc: runs as root"
  pass "$svc: runs as a non-root user"
}

echo "Building and starting containers…"
docker compose up -d --build --quiet-pull

check sqlite 18080
check postgres 18081

docker compose restart sqlite postgres >/dev/null
wait_for 60 "both answer again after a restart" sh -c \
  "curl -fsS http://$HOST:18080/healthz && curl -fsS http://$HOST:18081/healthz"

# Docker's own health check runs /eyrie healthcheck every 30 s.
wait_for 120 "Docker reports both healthy" both_healthy

echo
printf '\033[32mSmoke test passed.\033[0m\n'
