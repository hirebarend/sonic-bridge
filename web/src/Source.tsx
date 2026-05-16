import { useEffect, useRef, useState } from "react";
import { Mp3Encoder } from "lamejs";
import { wsUrl } from "./ws";

type Status = "idle" | "starting" | "live" | "error" | "ended";

const MP3_KBPS = 64;

function toArrayBuffer(view: ArrayBufferView): ArrayBuffer {
  const out = new ArrayBuffer(view.byteLength);
  new Uint8Array(out).set(new Uint8Array(view.buffer as ArrayBuffer, view.byteOffset, view.byteLength));
  return out;
}

function Source() {
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const sourceNodeRef = useRef<MediaStreamAudioSourceNode | null>(null);
  const workletNodeRef = useRef<AudioWorkletNode | null>(null);
  const encoderRef = useRef<Mp3Encoder | null>(null);

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
    try {
      const enc = encoderRef.current;
      const ws = wsRef.current;
      if (enc && ws?.readyState === WebSocket.OPEN) {
        const tail = enc.flush();
        if (tail.length > 0) ws.send(toArrayBuffer(tail));
      }
    } catch { /* ignore */ }
    try { audioCtxRef.current?.close(); } catch { /* ignore */ }
    closeWsSilently();
  }

  async function start() {
    setError(null);
    setStatus("starting");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
      });
      streamRef.current = stream;

      const AudioCtor: typeof AudioContext =
        window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      const audioCtx = new AudioCtor();
      audioCtxRef.current = audioCtx;
      if (audioCtx.state === "suspended") await audioCtx.resume();

      await audioCtx.audioWorklet.addModule("/recorder-worklet.js");

      const sampleRate = audioCtx.sampleRate;
      const encoder = new Mp3Encoder(1, sampleRate, MP3_KBPS);
      encoderRef.current = encoder;

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
      sourceNode.connect(workletNode);
      sourceNodeRef.current = sourceNode;
      workletNodeRef.current = workletNode;

      workletNode.port.onmessage = (e: MessageEvent<Float32Array>) => {
        const samples = e.data;
        const int16 = new Int16Array(samples.length);
        for (let i = 0; i < samples.length; i++) {
          const s = Math.max(-1, Math.min(1, samples[i]));
          int16[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
        }
        const mp3 = encoder.encodeBuffer(int16);
        if (mp3.length > 0 && ws.readyState === WebSocket.OPEN) {
          ws.send(toArrayBuffer(mp3));
        }
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
