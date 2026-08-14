#!/usr/bin/env bash
# Superseded by hot-reuse-session.sh (hot pool, window 03).
exec "$(cd "$(dirname "$0")" && pwd)/hot-reuse-session.sh" webdriver
