// Buffers Float32 input into Int16 PCM chunks and posts them via transferable ArrayBuffer.
// Source encodes each posted PCM chunk to 8-bit mu-law before sending it.
// Chunk size is 2400 samples = 50 ms at 48 kHz mono. Both sides assume that fixed rate.

const CHUNK_SAMPLES = 2400;

class RecorderProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buffer = new Int16Array(CHUNK_SAMPLES);
    this.written = 0;
  }

  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (!channel || channel.length === 0) return true;

    for (let i = 0; i < channel.length; i++) {
      const s = Math.max(-1, Math.min(1, channel[i]));
      this.buffer[this.written++] = s < 0 ? s * 0x8000 : s * 0x7fff;
      if (this.written >= CHUNK_SAMPLES) {
        const out = this.buffer.slice(0);
        this.port.postMessage(out.buffer, [out.buffer]);
        this.written = 0;
      }
    }
    return true;
  }
}

registerProcessor("recorder", RecorderProcessor);
