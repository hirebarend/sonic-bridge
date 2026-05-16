import { useEffect, useRef, useState } from "react";
import { PLAYBACK_MIME, wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

function Destination() {
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const mediaSourceRef = useRef<MediaSource | null>(null);
  const sourceBufferRef = useRef<SourceBuffer | null>(null);
  const queueRef = useRef<ArrayBuffer[]>([]);

  useEffect(() => {
    return () => {
      wsRef.current?.close();
      try {
        if (mediaSourceRef.current?.readyState === "open") {
          mediaSourceRef.current.endOfStream();
        }
      } catch {
        // ignore
      }
    };
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

  function flushQueue() {
    const sb = sourceBufferRef.current;
    if (!sb || sb.updating) return;
    const next = queueRef.current.shift();
    if (next) {
      try {
        sb.appendBuffer(next);
      } catch (err) {
        console.error("appendBuffer failed", err);
      }
    }
  }

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      if (!("MediaSource" in window) || !MediaSource.isTypeSupported(PLAYBACK_MIME)) {
        throw new Error(`browser does not support playback of ${PLAYBACK_MIME}`);
      }
      const audio = audioRef.current;
      if (!audio) throw new Error("audio element missing");

      const mediaSource = new MediaSource();
      mediaSourceRef.current = mediaSource;
      audio.src = URL.createObjectURL(mediaSource);

      await new Promise<void>((resolve) => {
        mediaSource.addEventListener("sourceopen", () => resolve(), { once: true });
      });

      const sb = mediaSource.addSourceBuffer(PLAYBACK_MIME);
      sb.mode = "sequence";
      sb.addEventListener("updateend", flushQueue);
      sb.addEventListener("error", (e) => console.error("source buffer error", e));
      sourceBufferRef.current = sb;

      const ws = new WebSocket(wsUrl("/destination"));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onmessage = (e) => {
        if (!(e.data instanceof ArrayBuffer)) return;
        queueRef.current.push(e.data);
        flushQueue();
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

      try {
        await audio.play();
      } catch (err) {
        console.warn("audio.play() rejected, will autoplay when ready", err);
      }
      setStatus("live");
    } catch (err) {
      console.error("destination start failed", err);
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
      closeWsSilently();
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
      {status === "idle" && (
        <button onClick={start}>start listening</button>
      )}
      <audio ref={audioRef} autoPlay playsInline />
      {error && <p className="error">{error}</p>}
    </section>
  );
}

export default Destination;
