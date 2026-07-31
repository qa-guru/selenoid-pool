#!/usr/bin/env bash
# Ensure a hot WebDriver session on warm-chrome with /login already open.
# Writes warm stand properties for the test JVM (Owner config overlay).
set -euo pipefail

WD_URL="${WARM_WD_URL:-http://warm-chrome-1:4444}"
PREOPEN_URL="${PREOPEN_URL:-https://reference-app.autotests.ai/login}"
OUT="${WARM_SESSION_PROPS:-tests/src/test/resources/config/reference_prod_e2e.properties}"
CAPS='{"capabilities":{"alwaysMatch":{"browserName":"chrome","goog:chromeOptions":{"args":["--headless=new","--disable-dev-shm-usage","--window-size=1740,1080"]}}}}'

wd() {
  local method="$1" path="$2"
  shift 2
  curl -sf -X "$method" "${WD_URL}${path}" \
    -H 'Content-Type: application/json' \
    "$@"
}

echo "warm preopen: wd=${WD_URL} url=${PREOPEN_URL}"

# Reuse existing session (chromedriver is single-session).
SESSION_ID="$(wd GET /sessions | sed -n 's/.*\"id\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' | head -1 || true)"
if [[ -z "${SESSION_ID}" ]]; then
  CREATE_JSON="$(wd POST /session -d "${CAPS}")" || {
    # Session race: drop stale and retry once
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

# Clean login form on the hot session.
wd DELETE "/session/${SESSION_ID}/cookie" >/dev/null || true
wd POST "/session/${SESSION_ID}/url" -d "{\"url\":\"${PREOPEN_URL}\"}" >/dev/null

mkdir -p "$(dirname "${OUT}")"
cat >"${OUT}" <<EOF
allureReportMode=none
attachBrowserConsoleLogs=false
attachHarLogs=false
attachLastScreenshot=false
attachPageSource=false
attachVideo=false
enableAllureSelenideListener=false
baseUrl=https://reference-app.autotests.ai/
apiBaseUrl=https://reference-app.autotests.ai/
browser=chrome
browserVersion=148.0
browserSize=1740x1080
headless=true
closeBrowserAfterEach=false
closeBrowserAfterAll=false
enableHar=false
enableVnc=false
enableVideo=false
remoteUrl=${WD_URL}/
warm.sessionId=${SESSION_ID}
skipOpen=true
logToConsole=false
selenideLogToConsole=false
rootLogLevel=warn
EOF

echo "warm preopen: wrote ${OUT}"
echo "WARM_SESSION_ID=${SESSION_ID}"
