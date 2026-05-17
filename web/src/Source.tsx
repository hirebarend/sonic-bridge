import { useCallback, useEffect, useRef, useState } from "react";
import { createAudioContext } from "./utils";
import { useWakeLock } from "./use-wake-lock";
import { useWebSocket } from "./use-web-socket";

const SAMPLE_RATE = 48000;

function getMediaStream() {
  return navigator.mediaDevices.getUserMedia({
    audio: {
      channelCount: 1,
      sampleRate: SAMPLE_RATE,
      echoCancellation: true,
      noiseSuppression: true,
    },
  });
}

function Source() {
  const [isRunning, setIsRunning] = useState(false);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const mediaStreamRef = useRef<MediaStream | null>(null);
  const mediaStreamAudioSourceNodeRef =
    useRef<MediaStreamAudioSourceNode | null>(null);
  const audioWorkletNodeRef = useRef<AudioWorkletNode | null>(null);

  const { close, connect, send, status } = useWebSocket("/source", {
    onError() {},
  });

  useWakeLock(isRunning);

  const cleanup = useCallback(() => {
    try {
      audioWorkletNodeRef.current?.disconnect();
    } catch {
      /* ignore */
    }
    try {
      mediaStreamAudioSourceNodeRef.current?.disconnect();
    } catch {
      /* ignore */
    }
    try {
      mediaStreamRef.current?.getTracks().forEach((t) => t.stop());
    } catch {
      /* ignore */
    }
    try {
      audioCtxRef.current?.close();
    } catch {
      /* ignore */
    }
    close();
    audioCtxRef.current = null;
    mediaStreamRef.current = null;
    mediaStreamAudioSourceNodeRef.current = null;
    audioWorkletNodeRef.current = null;
  }, [close]);

  useEffect(() => {
    return cleanup;
  }, [cleanup]);

  const buttonLabel = !isRunning
    ? "Start streaming"
    : status === "connecting"
      ? "Connecting"
      : status === "reconnecting"
        ? "Reconnecting"
        : "Streaming";

  async function start() {
    setIsRunning(true);

    try {
      const mediaStream = await getMediaStream();
      mediaStreamRef.current = mediaStream;

      const audioCtx = await createAudioContext();
      audioCtxRef.current = audioCtx;
      await audioCtx.audioWorklet.addModule("/recorder-worklet.js");

      await connect();

      const sourceNode = audioCtx.createMediaStreamSource(mediaStream);
      const workletNode = new AudioWorkletNode(audioCtx, "recorder");
      const silentSink = audioCtx.createGain();
      silentSink.gain.value = 0;
      sourceNode.connect(workletNode);
      workletNode.connect(silentSink);
      silentSink.connect(audioCtx.destination);
      mediaStreamAudioSourceNodeRef.current = sourceNode;
      audioWorkletNodeRef.current = workletNode;

      workletNode.port.onmessage = (e: MessageEvent<ArrayBuffer>) => {
        if (!(e.data instanceof ArrayBuffer)) {
          return;
        }

        if (!send(e.data)) {
          return;
        }
      };
    } catch {
      setIsRunning(false);
      cleanup();
    }
  }

  return (
    <button
      className="min-w-40 rounded-full border border-neutral-700 bg-neutral-900 px-6 py-3 text-sm font-medium text-neutral-50 shadow-sm transition hover:border-neutral-500 hover:bg-neutral-800 focus:outline-none focus:ring-2 focus:ring-neutral-400 focus:ring-offset-2 focus:ring-offset-neutral-950 disabled:cursor-default disabled:border-emerald-500/40 disabled:bg-emerald-500/10 disabled:text-emerald-200"
      disabled={isRunning}
      onClick={start}
    >
      {buttonLabel}
    </button>
  );
}

export default Source;
