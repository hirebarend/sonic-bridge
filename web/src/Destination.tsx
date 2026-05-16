import { useEffect, useRef, useState } from "react";
import { wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

const SAMPLE_RATE = 48000;
const SCHEDULE_AHEAD_SEC = 0.05;

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
      const audioCtx = new AudioCtor({ sampleRate: SAMPLE_RATE });
      audioCtxRef.current = audioCtx;
      if (audioCtx.state === "suspended") await audioCtx.resume();
      if (audioCtx.sampleRate !== SAMPLE_RATE) {
        throw new Error(`AudioContext rate mismatch: got ${audioCtx.sampleRate}, need ${SAMPLE_RATE}`);
      }

      const ws = new WebSocket(wsUrl("/destination"));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      let received = 0;
      ws.onmessage = (e) => {
        if (!(e.data instanceof ArrayBuffer)) return;
        received++;
        if (received === 1) console.log("destination: first PCM chunk", e.data.byteLength, "bytes");
        const int16 = new Int16Array(e.data);
        const float32 = new Float32Array(int16.length);
        for (let i = 0; i < int16.length; i++) {
          const v = int16[i];
          float32[i] = v < 0 ? v / 0x8000 : v / 0x7fff;
        }
        const buf = audioCtx.createBuffer(1, float32.length, SAMPLE_RATE);
        buf.getChannelData(0).set(float32);
        const node = audioCtx.createBufferSource();
        node.buffer = buf;
        node.connect(audioCtx.destination);
        const now = audioCtx.currentTime;
        const startAt = Math.max(now + SCHEDULE_AHEAD_SEC, nextStartTimeRef.current);
        if (nextStartTimeRef.current < now) {
          console.log("destination: resync, lag was", (now - nextStartTimeRef.current).toFixed(3), "s");
        }
        node.start(startAt);
        nextStartTimeRef.current = startAt + buf.duration;
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
        const onOpen = () => { cleanupListeners(); resolve(); };
        const onError = () => { cleanupListeners(); reject(new Error("websocket error")); };
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
