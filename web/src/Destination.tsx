import { useCallback, useEffect, useRef, useState } from "react";
import { createAudioContext, scheduleChunk } from "./utils";
import { useWakeLock } from "./use-wake-lock";
import { useWebSocket } from "./use-web-socket";

function Destination() {
  const [isRunning, setIsRunning] = useState(false);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const nextStartTimeRef = useRef(0);

  const { close, connect, status } = useWebSocket("/destination", {
    onMessage(data) {
      const audioCtx = audioCtxRef.current;

      if (!audioCtx) {
        return;
      }

      nextStartTimeRef.current = scheduleChunk(
        audioCtx,
        data,
        nextStartTimeRef.current,
      );
    },
    onClose() {
      nextStartTimeRef.current = 0;
    },
    onError() {},
  });

  useWakeLock(isRunning);

  const cleanup = useCallback(() => {
    close();

    try {
      audioCtxRef.current?.close();
    } catch {
      /* ignore */
    }

    audioCtxRef.current = null;
    nextStartTimeRef.current = 0;
  }, [close]);

  useEffect(() => {
    return cleanup;
  }, [cleanup]);

  const buttonLabel = !isRunning
    ? "Start listening"
    : status === "connecting"
      ? "Connecting"
      : status === "reconnecting"
        ? "Reconnecting"
        : "Listening";

  async function start() {
    setIsRunning(true);

    try {
      const audioCtx = await createAudioContext();
      audioCtxRef.current = audioCtx;
      nextStartTimeRef.current = 0;

      await connect();
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

export default Destination;
