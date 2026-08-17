#!/usr/bin/env bash
# Deprecated name. Hot session-reuse is POST /pool/lease (Go orchestrator).
exec "$(cd "$(dirname "$0")" && pwd)/lease.sh" "$@"
