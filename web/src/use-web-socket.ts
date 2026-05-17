import { useCallback, useEffect, useRef, useState } from "react";
import { wsUrl } from "./ws";

type WebSocketPath = "/source" | "/destination";
type WebSocketStatus = "idle" | "connecting" | "open" | "reconnecting" | "closed";

type WebSocketHandlers = {
  onMessage?: (data: ArrayBuffer, event: MessageEvent) => void;
  onOpen?: () => void;
  onClose?: (event: CloseEvent) => void;
  onError?: (event: Event) => void;
};

type WebSocketOptions = {
  autoReconnect?: boolean;
  reconnectInitialDelayMs?: number;
  reconnectMaxDelayMs?: number;
  reconnectBackoffFactor?: number;
};

const DEFAULT_RECONNECT_INITIAL_DELAY_MS = 500;
const DEFAULT_RECONNECT_MAX_DELAY_MS = 5_000;
const DEFAULT_RECONNECT_BACKOFF_FACTOR = 1.6;

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

export function useWebSocket(
  path: WebSocketPath,
  handlers: WebSocketHandlers,
  options: WebSocketOptions = {},
) {
  const handlersRef = useRef(handlers);
  const optionsRef = useRef(options);
  const wsRef = useRef<WebSocket | null>(null);
  const manualCloseRef = useRef(true);
  const reconnectAttemptRef = useRef(0);
  const reconnectTimerRef = useRef<number | null>(null);
  const scheduleReconnectRef = useRef<() => void>(() => {});
  const openSocketRef = useRef<(isReconnect: boolean) => Promise<void>>(
    async () => {},
  );
  const [status, setStatus] = useState<WebSocketStatus>("idle");

  useEffect(() => {
    handlersRef.current = handlers;
  }, [handlers]);

  useEffect(() => {
    optionsRef.current = options;
  }, [options]);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current === null) return;

    window.clearTimeout(reconnectTimerRef.current);
    reconnectTimerRef.current = null;
  }, []);

  const closeCurrentSocket = useCallback(() => {
    closeWebSocketSilently(wsRef.current);
    wsRef.current = null;
  }, []);

  const scheduleReconnect = useCallback(() => {
    if (manualCloseRef.current || reconnectTimerRef.current !== null) {
      return;
    }

    const {
      autoReconnect = true,
      reconnectInitialDelayMs = DEFAULT_RECONNECT_INITIAL_DELAY_MS,
      reconnectMaxDelayMs = DEFAULT_RECONNECT_MAX_DELAY_MS,
      reconnectBackoffFactor = DEFAULT_RECONNECT_BACKOFF_FACTOR,
    } = optionsRef.current;

    if (!autoReconnect) {
      setStatus("closed");
      return;
    }

    const delay = Math.min(
      reconnectMaxDelayMs,
      reconnectInitialDelayMs *
        reconnectBackoffFactor ** reconnectAttemptRef.current,
    );
    reconnectAttemptRef.current += 1;
    setStatus("reconnecting");

    reconnectTimerRef.current = window.setTimeout(() => {
      reconnectTimerRef.current = null;
      void openSocketRef.current(true).catch(() => {
        scheduleReconnectRef.current();
      });
    }, delay);
  }, []);

  const openSocket = useCallback(
    async (isReconnect: boolean) => {
      closeCurrentSocket();
      setStatus(isReconnect ? "reconnecting" : "connecting");

      let opened = false;
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

        if (!manualCloseRef.current && (opened || isReconnect)) {
          scheduleReconnect();
          return;
        }

        setStatus("closed");
      };
      ws.onerror = (event) => {
        handlersRef.current.onError?.(event);
      };

      await waitForWebSocketOpen(ws);

      opened = true;
      reconnectAttemptRef.current = 0;
      setStatus("open");
      handlersRef.current.onOpen?.();
    },
    [closeCurrentSocket, path, scheduleReconnect],
  );

  useEffect(() => {
    scheduleReconnectRef.current = scheduleReconnect;
  }, [scheduleReconnect]);

  useEffect(() => {
    openSocketRef.current = openSocket;
  }, [openSocket]);

  const close = useCallback(() => {
    manualCloseRef.current = true;
    clearReconnectTimer();
    closeCurrentSocket();
    reconnectAttemptRef.current = 0;
    setStatus("idle");
  }, [clearReconnectTimer, closeCurrentSocket]);

  const connect = useCallback(async () => {
    manualCloseRef.current = false;
    clearReconnectTimer();
    reconnectAttemptRef.current = 0;

    await openSocket(false);
  }, [clearReconnectTimer, openSocket]);

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

  return { close, connect, send, status };
}
