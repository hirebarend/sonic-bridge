// sonic-bridge wire format for the ESP32 firmware.
//
// This header is deliberately free of Arduino and ESP-IDF dependencies so the
// bit-level code can be compiled and verified on a development machine. Run
// scripts/check-wire-format.sh to confirm it agrees byte for byte with
// internal/audio (Go) and web/src/lib (TypeScript).

#ifndef SONIC_WIRE_H
#define SONIC_WIRE_H

#include <stddef.h>
#include <stdint.h>

namespace sonic {

// Codec ids must match codecIds in internal/audio/format.go. mu-law is the
// only codec the relay accepts.
enum CodecId : uint8_t {
    kCodecMulaw = 1,
};

// HeaderBytes must match audio.HeaderBytes in Go.
constexpr size_t kHeaderBytes = 12;

// buildStreamHeader writes the header a TCP source sends once, before any
// audio. The layout must match ParseHeader in internal/audio/format.go.
inline size_t buildStreamHeader(uint8_t* out, CodecId codec, uint32_t sampleRate, uint16_t frameSamples) {
    out[0] = 'S';
    out[1] = 'B';
    out[2] = '0';
    out[3] = '1';
    out[4] = static_cast<uint8_t>(codec);
    out[5] = 1;
    out[6] = static_cast<uint8_t>(frameSamples & 0xFF);
    out[7] = static_cast<uint8_t>((frameSamples >> 8) & 0xFF);
    out[8] = static_cast<uint8_t>(sampleRate & 0xFF);
    out[9] = static_cast<uint8_t>((sampleRate >> 8) & 0xFF);
    out[10] = static_cast<uint8_t>((sampleRate >> 16) & 0xFF);
    out[11] = static_cast<uint8_t>((sampleRate >> 24) & 0xFF);

    return kHeaderBytes;
}

// ITU-T G.711 mu-law. Mirror of internal/audio/codec.go. The segment table and
// the +132 bias are fixed by the standard.
constexpr int kMulawBias = 132;
constexpr int kMulawClip = 8159;
constexpr int kMulawSegmentCount = 8;
constexpr int kMulawSegmentEnds[kMulawSegmentCount] = {
    0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF,
};

inline int findMulawSegment(int magnitude) {
    for (int segment = 0; segment < kMulawSegmentCount; ++segment) {
        if (magnitude <= kMulawSegmentEnds[segment]) {
            return segment;
        }
    }

    return kMulawSegmentCount;
}

inline uint8_t encodeMulawSample(int16_t sample) {
    int magnitude = sample >> 2;
    int mask = 0xFF;

    if (magnitude < 0) {
        magnitude = -magnitude;
        mask = 0x7F;
    }

    if (magnitude > kMulawClip) {
        magnitude = kMulawClip;
    }

    magnitude += kMulawBias >> 2;

    const int segment = findMulawSegment(magnitude);

    if (segment >= kMulawSegmentCount) {
        return static_cast<uint8_t>(0x7F ^ mask);
    }

    return static_cast<uint8_t>(((segment << 4) | ((magnitude >> (segment + 1)) & 0x0F)) ^ mask);
}

// encodeFrame writes samples as mu-law and returns the byte count, which is
// one byte per sample. out must hold at least sampleCount bytes.
inline size_t encodeFrame(const int16_t* samples, size_t sampleCount, uint8_t* out) {
    for (size_t i = 0; i < sampleCount; ++i) {
        out[i] = encodeMulawSample(samples[i]);
    }

    return sampleCount;
}

}  // namespace sonic

#endif  // SONIC_WIRE_H
