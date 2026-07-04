#!/usr/bin/env bash
# Example Jenkins step: preopen URL on warm slot BEFORE Gradle starts.
# Run in parallel with ./gradlew or as a preceding lightweight stage.

set -euo pipefail

ORCHESTRATOR_URL="${WARM_POOL_URL:-http://127.0.0.1:9090}"
PREOPEN_URL="${PREOPEN_URL:-}"
OWNER="${BUILD_TAG:-local-$(date +%s)}"

if [[ -z "${PREOPEN_URL}" ]]; then
  echo "PREOPEN_URL is empty — skip warm preopen"
  exit 0
fi

reserve_json="$(curl -sf -X POST "${ORCHESTRATOR_URL}/pool/reserve" \
  -H 'Content-Type: application/json' \
  -d "{\"protocol\":\"webdriver\",\"browser\":\"chrome\",\"owner\":\"${OWNER}\"}")"

SLOT_ID="$(printf '%s' "${reserve_json}" | python -c "import sys,json; print(json.load(sys.stdin)['slot']['id'])")"
WEBDRIVER_URL="$(printf '%s' "${reserve_json}" | python -c "import sys,json; print(json.load(sys.stdin)['slot'].get('webdriverUrl') or '')")"

echo "WARM_SLOT_ID=${SLOT_ID}"
echo "Reserved slot ${SLOT_ID}"

curl -sf -X POST "${ORCHESTRATOR_URL}/pool/preopen" \
  -H 'Content-Type: application/json' \
  -d "{\"slotId\":\"${SLOT_ID}\",\"url\":\"${PREOPEN_URL}\"}"

curl -sf -X POST "${ORCHESTRATOR_URL}/pool/video/start" \
  -H 'Content-Type: application/json' \
  -d "{\"slotId\":\"${SLOT_ID}\",\"sessionId\":\"${SLOT_ID}\"}" || true

# Gradle uses reserved endpoint (parallel start is OK — page is already loading)
if [[ -n "${WEBDRIVER_URL}" ]]; then
  export SELENOID_URL="${WEBDRIVER_URL}"
  echo "SELENOID_URL=${WEBDRIVER_URL}"
fi

# After tests (separate post step):
# curl -sf -X POST "${ORCHESTRATOR_URL}/pool/video/stop" -H 'Content-Type: application/json' -d "{\"slotId\":\"${SLOT_ID}\"}"
# curl -sf -X POST "${ORCHESTRATOR_URL}/pool/release" -H 'Content-Type: application/json' -d "{\"slotId\":\"${SLOT_ID}\"}"
