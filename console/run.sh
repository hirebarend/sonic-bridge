#!/usr/bin/env bash
# Build and run the sonic-bridge console. Any args are forwarded to the binary
# (e.g. --host 192.168.1.50 --port 9001).
set -euo pipefail

cd "$(dirname "$0")"

if [[ ! -f build/CMakeCache.txt ]]; then
    cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
fi
cmake --build build
exec ./build/console "$@"
