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
    this.quiet = false;
    this.nextPosition = null;

    this.port.onmessage = (event) => {
      this.receive(event.data);
    };
  }

  reset() {
    this.readIndex = 0;
    this.writeIndex = 0;
    this.buffered = 0;
    this.playing = false;
    this.quiet = false;
    this.nextPosition = null;
  }

  receive(message) {
    if (message.type === "reset") {
      this.reset();
      return;
    }
    if (message.type === "quiet") {
      if (!this.quiet && this.nextPosition !== null && message.position > this.nextPosition) this.underruns += 1;
      this.quiet = true;
      // Keep the audio boundary; a heartbeat must not erase an undrained tail.
      this.nextPosition ??= message.position;
      return;
    }
    const { samples, position } = message;
    if (this.nextPosition !== null && position < this.nextPosition) return;
    if (this.nextPosition !== null && position > this.nextPosition) {
      const gap = position - this.nextPosition;
      if (this.quiet && this.buffered > 0 && gap + this.buffered + samples.length <= this.ring.length) {
        // Both boundaries arrived before the preceding tail played. Preserve
        // that tail and the short quiet interval between it and the new event.
        this.append(new Float32Array(gap));
      } else {
        if (!this.quiet) this.underruns += 1;
        // Long silence has already played locally. Never queue it as a backlog.
        this.reset();
      }
    }
    this.quiet = false;
    this.nextPosition = position + samples.length;
    this.append(samples);
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
      if (this.buffered < this.prefillSamples && !(this.quiet && this.buffered > 0)) {
        output.fill(0);

        return true;
      }

      this.playing = true;
    }

    for (let i = 0; i < output.length; i += 1) {
      if (this.buffered === 0) {
        output.fill(0, i);
        if (!this.quiet) this.underruns += 1;
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
