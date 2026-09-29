// Mirror of the mu-law decoder in internal/audio/codec.go.
//
// The browser only ever decodes: sources encode and the relay forwards. This is
// a separate module rather than inline script so that
// scripts/check-wire-format.sh can import it in Node and prove it agrees with
// the Go encoder byte for byte. A decoder that drifts from the encoder produces
// noise, not an error, so that check is the only thing standing between a
// one-character edit here and a silently broken stream.

const MULAW_BIAS = 132;

export function decodeMulaw(encoded) {
  const samples = new Int16Array(encoded.length);

  for (let i = 0; i < encoded.length; i += 1) {
    const inverted = ~encoded[i] & 0xff;
    let magnitude = ((inverted & 0x0f) << 3) + MULAW_BIAS;
    magnitude <<= (inverted & 0x70) >> 4;
    samples[i] = (inverted & 0x80) !== 0 ? MULAW_BIAS - magnitude : magnitude - MULAW_BIAS;
  }

  return samples;
}

export function toFloat32(samples) {
  const converted = new Float32Array(samples.length);

  for (let i = 0; i < samples.length; i += 1) {
    converted[i] = samples[i] / 32768;
  }

  return converted;
}
