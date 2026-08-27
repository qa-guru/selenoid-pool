#!/bin/bash
# Chromium + CDP inside qaguru/playwright-chromium:*-min (not Playwright WS :3000).
# Copied into selenoid-pool (not a symlink to greedy-guru).
set -e
perl /tmp/cdpproxy.pl &
bin=""
for c in /ms-playwright/chromium-*/chrome-linux64/chrome /ms-playwright/chromium-*/chrome-linux/chrome; do
  if [ -x "$c" ]; then bin=$c; break; fi
done
if [ -z "$bin" ]; then
  echo "no playwright chromium in image" >&2
  exit 1
fi
exec "$bin" \
  --headless=new \
  --disable-gpu \
  --no-sandbox \
  --disable-dev-shm-usage \
  --no-first-run \
  --no-default-browser-check \
  --disable-extensions \
  --disable-component-extensions-with-background-pages \
  --disable-background-networking \
  --disable-features=LocalNetworkAccessChecks,LocalNetworkAccessChecksWebRTC,PrivateNetworkAccessPermissionPrompt,BlockInsecurePrivateNetworkRequests \
  --remote-allow-origins=* \
  --user-data-dir=/tmp/hot-cdp \
  --remote-debugging-port=9222 \
  --remote-debugging-address=127.0.0.1 \
  about:blank
