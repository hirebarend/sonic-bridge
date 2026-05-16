import { useEffect, useRef, useState } from "react";
import { wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

const SCHEDULE_AHEAD_SEC = 0.1;

function Destination() {
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const nextStartTimeRef = useRef(0);

  useEffect(() => {
    return () => cleanup();
  }, []);

  function closeWsSilently() {
    const ws = wsRef.current;
    if (!ws) return;
    ws.onopen = null;
    ws.onmessage = null;
    ws.onerror = null;
    ws.onclose = null;
    try { ws.close(); } catch { /* ignore */ }
  }

  function cleanup() {
    closeWsSilently();
    try { audioCtxRef.current?.close(); } catch { /* ignore */ }
  }

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      const AudioCtor: typeof AudioContext =
        window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      const audioCtx = new AudioCtor();
      audioCtxRef.current = audioCtx;
      if (audioCtx.state === "suspended") await audioCtx.resume();

      const ws = new WebSocket(wsUrl("/destination"));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onmessage = async (e) => {
        if (!(e.data instanceof ArrayBuffer)) return;
        try {
          const audioBuffer = await audioCtx.decodeAudioData(e.data);
          const node = audioCtx.createBufferSource();
          node.buffer = audioBuffer;
          node.connect(audioCtx.destination);
          const now = audioCtx.currentTime;
          const startAt = Math.max(now + SCHEDULE_AHEAD_SEC, nextStartTimeRef.current);
          node.start(startAt);
          nextStartTimeRef.current = startAt + audioBuffer.duration;
        } catch (err) {
          console.error("decode/play failed", err);
        }
      };
      ws.onclose = (ev) => {
        console.log("destination ws closed", ev.code, ev.reason);
        setStatus("ended");
        if (ev.code !== 1000) setError(`server closed: ${ev.code} ${ev.reason || ""}`.trim());
      };
      ws.onerror = (ev) => {
        console.error("destination ws error", ev);
        setError("websocket error");
        setStatus("error");
      };

      await new Promise<void>((resolve, reject) => {
        if (ws.readyState === WebSocket.OPEN) return resolve();
        const onOpen = () => {
          cleanupListeners();
          resolve();
        };
        const onError = () => {
          cleanupListeners();
          reject(new Error("websocket error"));
        };
        const cleanupListeners = () => {
          ws.removeEventListener("open", onOpen);
          ws.removeEventListener("error", onError);
        };
        ws.addEventListener("open", onOpen);
        ws.addEventListener("error", onError);
      });

      setStatus("live");
    } catch (err) {
      console.error("destination start failed", err);
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
      cleanup();
    }
  }

  return (
    <section className="panel">
      <p className="role">destination</p>
      <p className={`status status-${status}`}>
        {status === "idle" && "ready"}
        {status === "starting" && "starting…"}
        {status === "live" && "live"}
        {status === "ended" && "ended"}
        {status === "error" && "error"}
      </p>
      {status === "idle" && <button onClick={start}>start listening</button>}
      {error && <p className="error">{error}</p>}
    </section>
  );
}

export default Destination;
