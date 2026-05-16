import { useEffect, useRef, useState } from "react";
import { wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

const SAMPLE_RATE = 48000;

function Source() {
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const sourceNodeRef = useRef<MediaStreamAudioSourceNode | null>(null);
  const workletNodeRef = useRef<AudioWorkletNode | null>(null);

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
    try { workletNodeRef.current?.disconnect(); } catch { /* ignore */ }
    try { sourceNodeRef.current?.disconnect(); } catch { /* ignore */ }
    try { streamRef.current?.getTracks().forEach((t) => t.stop()); } catch { /* ignore */ }
    try { audioCtxRef.current?.close(); } catch { /* ignore */ }
    closeWsSilently();
  }

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          sampleRate: SAMPLE_RATE,
          echoCancellation: true,
          noiseSuppression: true,
        },
      });
      streamRef.current = stream;

      const AudioCtor: typeof AudioContext =
        window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      const audioCtx = new AudioCtor({ sampleRate: SAMPLE_RATE });
      audioCtxRef.current = audioCtx;
      if (audioCtx.state === "suspended") await audioCtx.resume();
      if (audioCtx.sampleRate !== SAMPLE_RATE) {
        throw new Error(`AudioContext rate mismatch: got ${audioCtx.sampleRate}, need ${SAMPLE_RATE}`);
      }

      await audioCtx.audioWorklet.addModule("/recorder-worklet.js");

      const ws = new WebSocket(wsUrl("/source"));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      await new Promise<void>((resolve, reject) => {
        ws.onopen = () => resolve();
        ws.onerror = () => reject(new Error("websocket error"));
        ws.onclose = (ev) =>
          reject(new Error(`websocket closed before open: ${ev.code} ${ev.reason || ""}`.trim()));
      });

      ws.onerror = (ev) => console.error("source ws error", ev);
      ws.onclose = (ev) => {
        console.log("source ws closed", ev.code, ev.reason);
        setStatus("ended");
        if (ev.code !== 1000) setError(`server closed: ${ev.code} ${ev.reason || ""}`.trim());
        try { workletNodeRef.current?.disconnect(); } catch { /* ignore */ }
        streamRef.current?.getTracks().forEach((t) => t.stop());
      };

      const sourceNode = audioCtx.createMediaStreamSource(stream);
      const workletNode = new AudioWorkletNode(audioCtx, "recorder");
      const silentSink = audioCtx.createGain();
      silentSink.gain.value = 0;
      sourceNode.connect(workletNode);
      workletNode.connect(silentSink);
      silentSink.connect(audioCtx.destination);
      sourceNodeRef.current = sourceNode;
      workletNodeRef.current = workletNode;

      let chunks = 0;
      workletNode.port.onmessage = (e: MessageEvent<ArrayBuffer>) => {
        if (!(e.data instanceof ArrayBuffer)) return;
        if (ws.readyState !== WebSocket.OPEN) return;
        chunks++;
        if (chunks === 1) console.log("source: first PCM chunk", e.data.byteLength, "bytes");
        ws.send(e.data);
      };

      setStatus("live");
    } catch (err) {
      console.error("source start failed", err);
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
      cleanup();
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
      {status === "idle" && <button onClick={start}>start streaming</button>}
      {error && <p className="error">{error}</p>}
    </section>
  );
}

export default Source;
