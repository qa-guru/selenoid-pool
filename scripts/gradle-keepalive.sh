#!/usr/bin/env bash
# Keep Gradle daemon alive across Jenkins builds (ProcessTreeKiller bypass).
set -euo pipefail
export BUILD_ID=dontKillMe
export JENKINS_NODE_COOKIE=dontKillMe
WS=${1:-/home/jenkins/agent/workspace/autotests-ai-multistack-tests-pipeline-java-warm-pool/tests}
if [[ -x "$WS/gradlew" ]]; then
  cd "$WS"
  ./gradlew --daemon help -q
  ./gradlew --status || true
else
  echo "gradle keepalive: workspace not ready yet: $WS"
fi
