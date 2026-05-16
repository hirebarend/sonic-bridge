import { useEffect, useRef, useState } from "react";
import { MIME, wsUrl } from "./ws";

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

  function flushQueue() {
    const sb = sourceBufferRef.current;
    if (!sb || sb.updating) return;
    const next = queueRef.current.shift();
    if (next) sb.appendBuffer(next);
  }

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      if (!("MediaSource" in window) || !MediaSource.isTypeSupported(MIME)) {
        throw new Error(`browser does not support ${MIME}`);
      }
      const audio = audioRef.current;
      if (!audio) throw new Error("audio element missing");

      const mediaSource = new MediaSource();
      mediaSourceRef.current = mediaSource;
      audio.src = URL.createObjectURL(mediaSource);

      await new Promise<void>((resolve) => {
        mediaSource.addEventListener("sourceopen", () => resolve(), { once: true });
      });

      const sb = mediaSource.addSourceBuffer(MIME);
      sb.mode = "sequence";
      sb.addEventListener("updateend", flushQueue);
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
        setStatus("ended");
        if (ev.code !== 1000) setError(`server closed: ${ev.code} ${ev.reason || ""}`.trim());
      };
      ws.onerror = () => {
        setError("websocket error");
        setStatus("error");
      };

      await audio.play();
      setStatus("live");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
      wsRef.current?.close();
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
