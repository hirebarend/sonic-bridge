import { useCallback, useEffect, useRef } from "react";
import { wsUrl } from "./ws";

type WebSocketPath = "/source" | "/destination";

type WebSocketHandlers = {
  onMessage?: (data: ArrayBuffer, event: MessageEvent) => void;
  onClose?: (event: CloseEvent) => void;
  onError?: (event: Event) => void;
};

function closeWebSocketSilently(ws: WebSocket | null) {
  if (!ws) return;

  ws.onopen = null;
  ws.onmessage = null;
  ws.onerror = null;
  ws.onclose = null;

  try {
    ws.close();
  } catch {
    /* ignore */
  }
}

function waitForWebSocketOpen(ws: WebSocket) {
  return new Promise<void>((resolve, reject) => {
    if (ws.readyState === WebSocket.OPEN) {
      resolve();
      return;
    }

    function cleanupListeners() {
      ws.removeEventListener("open", onOpen);
      ws.removeEventListener("error", onError);
      ws.removeEventListener("close", onClose);
    }
    function onOpen() {
      cleanupListeners();
      resolve();
    }
    function onError() {
      cleanupListeners();
      reject(new Error("websocket error"));
    }
    function onClose(ev: CloseEvent) {
      cleanupListeners();
      reject(
        new Error(
          `websocket closed before open: ${ev.code} ${ev.reason || ""}`.trim(),
        ),
      );
    }

    ws.addEventListener("open", onOpen);
    ws.addEventListener("error", onError);
    ws.addEventListener("close", onClose);
  });
}

export function useWebSocket(path: WebSocketPath, handlers: WebSocketHandlers) {
  const handlersRef = useRef(handlers);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    handlersRef.current = handlers;
  }, [handlers]);

  const close = useCallback(() => {
    closeWebSocketSilently(wsRef.current);
    wsRef.current = null;
  }, []);

  const connect = useCallback(async () => {
    close();

    const ws = new WebSocket(wsUrl(path));
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onmessage = (event) => {
      if (!(event.data instanceof ArrayBuffer)) return;
      handlersRef.current.onMessage?.(event.data, event);
    };
    ws.onclose = (event) => {
      if (wsRef.current === ws) wsRef.current = null;
      handlersRef.current.onClose?.(event);
    };
    ws.onerror = (event) => {
      handlersRef.current.onError?.(event);
    };

    await waitForWebSocketOpen(ws);
  }, [close, path]);

  const send = useCallback((data: ArrayBuffer) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;

    try {
      ws.send(data);
      return true;
    } catch {
      return false;
    }
  }, []);

  useEffect(() => close, [close]);

  return { close, connect, send };
}
