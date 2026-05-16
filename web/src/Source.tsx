import { useEffect, useRef, useState } from "react";
import { MIME, wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

function Source() {
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);

  useEffect(() => {
    return () => {
      recorderRef.current?.stop();
      streamRef.current?.getTracks().forEach((t) => t.stop());
      wsRef.current?.close();
    };
  }, []);

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      if (!window.MediaRecorder || !MediaRecorder.isTypeSupported(MIME)) {
        throw new Error(`browser does not support ${MIME}`);
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;

      const ws = new WebSocket(wsUrl("/source"));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      await new Promise<void>((resolve, reject) => {
        ws.onopen = () => resolve();
        ws.onerror = () => reject(new Error("websocket error"));
        ws.onclose = (ev) => {
          if (status !== "live") reject(new Error(`websocket closed: ${ev.code} ${ev.reason}`));
        };
      });

      ws.onclose = (ev) => {
        setStatus("ended");
        if (ev.code !== 1000) setError(`server closed: ${ev.code} ${ev.reason || ""}`.trim());
        recorderRef.current?.stop();
        streamRef.current?.getTracks().forEach((t) => t.stop());
      };

      const recorder = new MediaRecorder(stream, { mimeType: MIME });
      recorderRef.current = recorder;
      recorder.ondataavailable = async (e) => {
        if (e.data.size === 0) return;
        if (ws.readyState !== WebSocket.OPEN) return;
        const buf = await e.data.arrayBuffer();
        ws.send(buf);
      };
      recorder.start(100);

      setStatus("live");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
      streamRef.current?.getTracks().forEach((t) => t.stop());
      wsRef.current?.close();
    }
  }

  return (
    <section className="panel">
      <p className="role">source</p>
      <p className={`status status-${status}`}>
        {status === "idle" && "ready"}
        {status === "starting" && "starting…"}
        {status === "live" && "live"}
        {status === "ended" && "ended"}
        {status === "error" && "error"}
      </p>
      {status === "idle" && (
        <button onClick={start}>start streaming</button>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}

export default Source;
