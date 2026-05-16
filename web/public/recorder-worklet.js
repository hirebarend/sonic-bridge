class RecorderProcessor extends AudioWorkletProcessor {
  process(inputs) {
    const channels = inputs[0];
    if (channels && channels[0] && channels[0].length > 0) {
      // Post a copy because the underlying buffer is reused next render quantum.
      this.port.postMessage(channels[0].slice(0));
    }
    return true;
  }
}

registerProcessor("recorder", RecorderProcessor);
