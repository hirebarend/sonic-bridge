const MU_LAW_BIAS = 0x84;
const MU_LAW_CLIP = 32635;
const DOWNSAMPLE_FACTOR = 3;

export const MU_LAW_SAMPLE_RATE = 16000;

function pcm16SampleToMuLaw(sample: number) {
  const sign = (sample >> 8) & 0x80;

  if (sign !== 0) {
    sample = -sample;
  }

  sample = Math.min(sample, MU_LAW_CLIP) + MU_LAW_BIAS;

  let exponent = 7;
  for (
    let exponentMask = 0x4000;
    (sample & exponentMask) === 0 && exponent > 0;
    exponentMask >>= 1
  ) {
    exponent -= 1;
  }

  const mantissa = (sample >> (exponent + 3)) & 0x0f;
  return ~(sign | (exponent << 4) | mantissa) & 0xff;
}

function muLawSampleToPcm16(muLawSample: number) {
  const value = ~muLawSample & 0xff;
  const sign = value & 0x80;
  const exponent = (value >> 4) & 0x07;
  const mantissa = value & 0x0f;

  let sample = ((mantissa << 3) + MU_LAW_BIAS) << exponent;
  sample -= MU_LAW_BIAS;

  return sign === 0 ? sample : -sample;
}

export function encodePcm16ToMuLaw16Khz(data: ArrayBuffer) {
  const pcm16 = new Int16Array(data);
  const encodedSampleCount = Math.floor(pcm16.length / DOWNSAMPLE_FACTOR);
  const encodedBuffer = new ArrayBuffer(encodedSampleCount);
  const encoded = new Uint8Array(encodedBuffer);

  for (let i = 0; i < encoded.length; i++) {
    const sourceIndex = i * DOWNSAMPLE_FACTOR;
    const downsampled = Math.trunc(
      (pcm16[sourceIndex] +
        pcm16[sourceIndex + 1] +
        pcm16[sourceIndex + 2]) /
        DOWNSAMPLE_FACTOR,
    );

    encoded[i] = pcm16SampleToMuLaw(downsampled);
  }

  return encodedBuffer;
}

export function decodeMuLawToFloat32(data: ArrayBuffer) {
  const muLaw = new Uint8Array(data);
  const float32 = new Float32Array(muLaw.length);

  for (let i = 0; i < muLaw.length; i++) {
    const sample = muLawSampleToPcm16(muLaw[i]);
    float32[i] = sample < 0 ? sample / 0x8000 : sample / 0x7fff;
  }

  return float32;
}
