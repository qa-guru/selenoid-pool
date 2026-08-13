#!/usr/bin/env bash
# Ensure a hot WebDriver session on warm-chrome with /login already open.
# Prints WARM_SESSION_ID=... for the caller; does NOT rewrite Owner stand files
# (keeps processTestResources UP-TO-DATE). Pass session via -Dwarm.sessionId=.
set -euo pipefail

WD_URL="${WARM_WD_URL:-http://warm-chrome-1:4444}"
PREOPEN_URL="${PREOPEN_URL:-https://autotests.ai/stack/backend-java-spring/frontend-typescript-react/login}"
CAPS='{"capabilities":{"alwaysMatch":{"browserName":"chrome","goog:chromeOptions":{"args":["--headless=new","--disable-dev-shm-usage","--window-size=1740,1080"]}}}}'
ENV_OUT="${WARM_SESSION_ENV:-warm-session.env}"

wd() {
  local method="$1" path="$2"
  shift 2
  curl -sf -X "$method" "${WD_URL}${path}" \
    -H 'Content-Type: application/json' \
    "$@"
}

echo "warm preopen: wd=${WD_URL} url=${PREOPEN_URL}"

SESSION_ID="$(wd GET /sessions | sed -n 's/.*\"id\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' | head -1 || true)"
if [[ -z "${SESSION_ID}" ]]; then
  CREATE_JSON="$(wd POST /session -d "${CAPS}")" || {
    OLD="$(wd GET /sessions | sed -n 's/.*\"id\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' | head -1 || true)"
    if [[ -n "${OLD}" ]]; then
      wd DELETE "/session/${OLD}" >/dev/null || true
    fi
    CREATE_JSON="$(wd POST /session -d "${CAPS}")"
  }
  SESSION_ID="$(printf '%s' "${CREATE_JSON}" | sed -n 's/.*\"sessionId\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' | head -1)"
  if [[ -z "${SESSION_ID}" ]]; then
    SESSION_ID="$(printf '%s' "${CREATE_JSON}" | sed -n 's/.*\"id\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' | head -1)"
  fi
fi

if [[ -z "${SESSION_ID}" ]]; then
  echo "warm preopen: failed to obtain sessionId" >&2
  echo "${CREATE_JSON:-}" >&2
  exit 1
fi

echo "warm preopen: sessionId=${SESSION_ID}"

wd DELETE "/session/${SESSION_ID}/cookie" >/dev/null || true
wd POST "/session/${SESSION_ID}/url" -d "{\"url\":\"${PREOPEN_URL}\"}" >/dev/null

cat >"${ENV_OUT}" <<EOF
WARM_SESSION_ID=${SESSION_ID}
WARM_WD_URL=${WD_URL}
PREOPEN_URL=${PREOPEN_URL}
EOF

echo "warm preopen: wrote ${ENV_OUT}"
echo "WARM_SESSION_ID=${SESSION_ID}"
