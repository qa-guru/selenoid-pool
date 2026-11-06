#!/usr/bin/env bash
# Version-drift audit: catalog defaults vs running pool slot images.
# Run on the selenoid host (needs docker + /opt/selenoid/browsers.json) or
# via:  ssh selenoid-prod 'bash -s' < deploy/check-version-drift.sh
set -u

CATALOG=/opt/selenoid/browsers.json
fail=0

default_of() { # catalog key -> default
  python3 -c "import json,sys;print(json.load(open('$CATALOG'))['$1']['default'])"
}

check() { # container catalog-key
  local name=$1 key=$2 tag def major
  tag=$(sudo docker inspect "$name" --format '{{.Config.Image}}' 2>/dev/null | cut -d: -f2) || return 0
  [ -z "$tag" ] && return 0
  def=$(default_of "$key")
  # image tags may drop the .0 patch (webdriver-chrome:157 vs default 157.0)
  case "$tag" in
    "$def"|"$def-min"|"${def%.0}"|"${def%.0}-min") echo "OK   $name  $tag (default $def)" ;;
    *) echo "DRIFT $name  $tag != catalog default $def"; fail=1 ;;
  esac
}

for c in $(sudo docker ps --format '{{.Names}}'); do
  case "$c" in
    warm-chrome-*|warm-pw-*|hot-pw-*|hot-cdp-*|hot-chrome-*) check "$c" "$( [ "${c#warm-pw}" != "$c" ] || [ "${c#hot-pw}" != "$c" ] || [ "${c#hot-cdp}" != "$c" ] && echo playwright-chromium || echo chrome)" ;;
  esac
done
exit "$fail"
