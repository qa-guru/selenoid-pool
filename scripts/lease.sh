#!/usr/bin/env bash
# Hot lease via the Go orchestrator. No Python.
# Usage: lease.sh [webdriver|playwright]
set -euo pipefail

ORCH="${WARM_POOL_URL:-http://127.0.0.1:9090}"
OWNER="${BUILD_TAG:-lease-cli}"
PROTO="${1:-webdriver}"
URL="${PREOPEN_URL:-}"

case "$PROTO" in
  webdriver|wd)
    PROTOCOL=webdriver
    BROWSER=chrome
    ;;
  playwright|pw)
    PROTOCOL=playwright
    BROWSER=chromium
    ;;
  *)
    echo "usage: $0 [webdriver|playwright]" >&2
    exit 2
    ;;
esac

json='{"pool":"hot","protocol":"'"$PROTOCOL"'","browser":"'"$BROWSER"'","owner":"'"$OWNER"'","loopback":true'
if [[ -n "$URL" ]]; then
  json+=',"url":"'"$URL"'"'
fi
json+='}'

exec curl -sS -X POST "$ORCH/pool/lease" \
  -H 'Content-Type: application/json' \
  -d "$json"
