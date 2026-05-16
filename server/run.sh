#!/usr/bin/env bash
# Build and run the sonic-bridge server. Any args are forwarded to the binary.
set -euo pipefail

cd "$(dirname "$0")"

mkdir -p bin
go build -o bin/server ./cmd/server
exec ./bin/server "$@"
