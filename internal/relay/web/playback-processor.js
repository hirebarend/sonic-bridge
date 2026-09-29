// Jitter buffer for the destination. Decoded frames arrive from the main
// thread at network pace; the audio device pulls at a fixed rate. This ring
// buffer absorbs the difference.
//
// It waits for a prefill before it starts, so the first pull does not
// underrun, and it re-arms that wait after any gap, so a network stall costs
// one silence instead of a run of clicks. When the buffer is full the oldest
// samples go first, which is the same drop-oldest policy internal/queue uses:
// latency is never allowed to grow without bound.

const STATS_INTERVAL_QUANTA = 16;

class PlaybackProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();

    const { ringSamples, prefillSamples } = options.processorOptions;

    this.ring = new Float32Array(ringSamples);
    this.prefillSamples = prefillSamples;
    this.readIndex = 0;
    this.writeIndex = 0;
    this.buffered = 0;
    this.playing = false;
    this.underruns = 0;
    this.dropped = 0;
    this.quantaSinceReport = 0;

    this.port.onmessage = (event) => {
      this.append(event.data);
    };
  }

  append(samples) {
    for (let i = 0; i < samples.length; i += 1) {
      if (this.buffered === this.ring.length) {
        this.readIndex = (this.readIndex + 1) % this.ring.length;
        this.buffered -= 1;
        this.dropped += 1;
      }

      this.ring[this.writeIndex] = samples[i];
      this.writeIndex = (this.writeIndex + 1) % this.ring.length;
      this.buffered += 1;
    }
  }

  process(_inputs, outputs) {
    const output = outputs[0][0];

    this.report();

    if (!this.playing) {
      if (this.buffered < this.prefillSamples) {
        output.fill(0);

        return true;
      }

      this.playing = true;
    }

    for (let i = 0; i < output.length; i += 1) {
      if (this.buffered === 0) {
        output.fill(0, i);
        this.underruns += 1;
        this.playing = false;

        return true;
      }

      output[i] = this.ring[this.readIndex];
      this.readIndex = (this.readIndex + 1) % this.ring.length;
      this.buffered -= 1;
    }

    return true;
  }

  report() {
    this.quantaSinceReport += 1;

    if (this.quantaSinceReport < STATS_INTERVAL_QUANTA) {
      return;
    }

    this.quantaSinceReport = 0;
    this.port.postMessage({
      bufferedSamples: this.buffered,
      underruns: this.underruns,
      dropped: this.dropped,
      playing: this.playing,
    });
  }
}

registerProcessor("playback-processor", PlaybackProcessor);
