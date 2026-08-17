#!/usr/bin/env bash
# Deprecated name. Use scripts/lease.sh (POST /pool/lease).
exec "$(cd "$(dirname "$0")" && pwd)/lease.sh" "$@"
