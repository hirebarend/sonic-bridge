#!/usr/bin/env bash
#
# Verifies that the three implementations of the sonic-bridge wire format agree
# wherever they overlap:
#
#   Go    internal/audio               the relay and console: encode and decode
#   C++   esp32/src/wire.h             the firmware: encodes only
#   JS    internal/relay/web/codec.js  the browser player: decodes only
#
# Go is the hub, so the honest check is two pairs against Go rather than one
# three-way comparison. Nothing requires C++ and JS to agree directly, because
# no firmware byte reaches a browser without passing through Go.
#
#   Pair 1   C++ encode plus stream header   against   Go encode plus header
#   Pair 2   JS decode                       against   Go decode
#
# A decoder that drifts from its encoder produces noise, not an error, so this
# is the only thing standing between a one-character edit and a silently broken
# stream. It needs go, node and a C++ compiler. It does not need an ESP32 or
# PlatformIO, because esp32/src/wire.h has no framework dependencies.

set -Eeuo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly WORK="$(mktemp -d)"

trap 'rm -rf "$WORK"' EXIT

log() {
  printf '==> %s\n' "$*"
}

fatal() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum <"$1" | cut -d' ' -f1
  else
    shasum -a 256 <"$1" | cut -d' ' -f1
  fi
}

log "Go: encode over every 16-bit sample, plus the stream header"
SONIC_WIRE_ENCODE_OUT="$WORK/go-encode.bin" \
  go -C "$ROOT" test -run TestWriteWireEncodeArtefact -count=1 ./internal/audio/ >/dev/null ||
  fatal "the Go encode artefact could not be produced"

log "Go: decode over all 256 mu-law codes"
SONIC_WIRE_DECODE_OUT="$WORK/go-decode.bin" \
  go -C "$ROOT" test -run TestWriteWireDecodeArtefact -count=1 ./internal/audio/ >/dev/null ||
  fatal "the Go decode artefact could not be produced"

log "C++: esp32/src/wire.h"
cat >"$WORK/encode.cpp" <<'CPP'
#include <cstdint>
#include <cstdio>
#include <vector>

#include "wire.h"

int main() {
    std::vector<std::uint8_t> encoded(65536);
    for (int i = 0; i < 65536; ++i) {
        encoded[static_cast<std::size_t>(i)] =
            sonic::encodeMulawSample(static_cast<std::int16_t>(i - 32768));
    }

    std::uint8_t header[sonic::kHeaderBytes];
    sonic::buildStreamHeader(header, sonic::kCodecMulaw, 16000, 320);

    std::fwrite(encoded.data(), 1, encoded.size(), stdout);
    std::fwrite(header, 1, sizeof(header), stdout);

    return 0;
}
CPP

c++ -std=c++17 -O2 -Wall -Wextra -I "$ROOT/esp32/src" \
  -o "$WORK/encode" "$WORK/encode.cpp" ||
  fatal "esp32/src/wire.h does not compile"

"$WORK/encode" >"$WORK/cpp-encode.bin"

log "JS: internal/relay/web/codec.js"
cat >"$WORK/decode.mjs" <<'JS'
import { writeFileSync } from "node:fs";

const [, , outPath, codecUrl] = process.argv;
const { decodeMulaw } = await import(codecUrl);

const codes = new Uint8Array(256);
for (let i = 0; i < codes.length; i += 1) {
  codes[i] = i;
}

const samples = decodeMulaw(codes);
const payload = Buffer.alloc(samples.length * 2);

for (let i = 0; i < samples.length; i += 1) {
  payload.writeInt16LE(samples[i], i * 2);
}

writeFileSync(outPath, payload);
JS

node "$WORK/decode.mjs" "$WORK/js-decode.bin" "file://$ROOT/internal/relay/web/codec.js" ||
  fatal "the JavaScript decode artefact could not be produced"

go_encode="$(digest "$WORK/go-encode.bin")"
cpp_encode="$(digest "$WORK/cpp-encode.bin")"
go_decode="$(digest "$WORK/go-decode.bin")"
js_decode="$(digest "$WORK/js-decode.bin")"

printf '\n  encode  Go   %s\n' "$go_encode"
printf '          C++  %s\n\n' "$cpp_encode"
printf '  decode  Go   %s\n' "$go_decode"
printf '          JS   %s\n\n' "$js_decode"

[[ "$go_encode" == "$cpp_encode" ]] ||
  fatal "Go and C++ encode differently: the firmware and the relay would exchange noise"
[[ "$go_decode" == "$js_decode" ]] ||
  fatal "Go and JS decode differently: the browser would play noise"

log "Both pairs agree, over every 16-bit sample and every mu-law code."
