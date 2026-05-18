import { decodeMuLawToFloat32 } from "./audio-codec";

type WindowWithWebkitAudioContext = Window &
  typeof globalThis & {
    webkitAudioContext?: typeof AudioContext;
  };

const SAMPLE_RATE = 48000;
const SCHEDULE_AHEAD_SEC = 0.05;

export async function createAudioContext(): Promise<AudioContext> {
  const AudioCtor =
    window.AudioContext ||
    (window as WindowWithWebkitAudioContext).webkitAudioContext;

  if (!AudioCtor) {
    throw new Error("Web Audio API is not supported");
  }

  const audioCtx = new AudioCtor({ sampleRate: SAMPLE_RATE });

  if (audioCtx.state === "suspended") {
    await audioCtx.resume();
  }

  if (audioCtx.sampleRate !== SAMPLE_RATE) {
    throw new Error(
      `AudioContext rate mismatch: got ${audioCtx.sampleRate}, need ${SAMPLE_RATE}`,
    );
  }

  return audioCtx;
}

export function scheduleChunk(
  audioCtx: AudioContext,
  data: ArrayBuffer,
  nextStartTime: number,
) {
  const float32 = decodeMuLawToFloat32(data);
  const buffer = audioCtx.createBuffer(1, float32.length, SAMPLE_RATE);
  buffer.getChannelData(0).set(float32);

  const node = audioCtx.createBufferSource();
  node.buffer = buffer;

  node.connect(audioCtx.destination);

  const now = audioCtx.currentTime;
  const startAt = Math.max(now + SCHEDULE_AHEAD_SEC, nextStartTime);

  if (nextStartTime < now) {
    console.log(
      "destination: resync, lag was",
      (now - nextStartTime).toFixed(3),
      "s",
    );
  }

  node.start(startAt);
  return startAt + buffer.duration;
}
