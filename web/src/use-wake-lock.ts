import { useCallback, useEffect, useMemo, useRef, useState } from "react";

type WakeLockType = "screen";

type WakeLockSentinel = EventTarget & {
  readonly released: boolean;
  readonly type: WakeLockType;
  release(): Promise<void>;
};

type NavigatorWithWakeLock = Navigator & {
  wakeLock?: {
    request(type: WakeLockType): Promise<WakeLockSentinel>;
  };
};

export function useWakeLock(enabled: boolean) {
  const sentinelRef = useRef<WakeLockSentinel | null>(null);
  const [isActive, setIsActive] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const isSupported = useMemo(
    () => "wakeLock" in navigator && window.isSecureContext,
    [],
  );

  const release = useCallback(async () => {
    const sentinel = sentinelRef.current;
    sentinelRef.current = null;

    if (!sentinel || sentinel.released) {
      setIsActive(false);
      return;
    }

    try {
      await sentinel.release();
    } catch {
      /* ignore */
    } finally {
      setIsActive(false);
    }
  }, []);

  const request = useCallback(async () => {
    if (!isSupported || document.visibilityState !== "visible") {
      return false;
    }

    try {
      const wakeLock = (navigator as NavigatorWithWakeLock).wakeLock;
      const sentinel = await wakeLock?.request("screen");

      if (!sentinel) {
        setIsActive(false);
        return false;
      }

      await release();
      sentinelRef.current = sentinel;
      sentinel.addEventListener("release", () => {
        if (sentinelRef.current !== sentinel) return;

        sentinelRef.current = null;
        setIsActive(false);
      });
      setError(null);
      setIsActive(true);
      return true;
    } catch (err) {
      setError(
        err instanceof Error ? err : new Error("Failed to request wake lock"),
      );
      setIsActive(false);
      return false;
    }
  }, [isSupported, release]);

  useEffect(() => {
    let requestTimer: number | null = null;
    let releaseTimer: number | null = null;

    if (!enabled) {
      releaseTimer = window.setTimeout(() => {
        void release();
      }, 0);

      return () => {
        if (releaseTimer !== null) window.clearTimeout(releaseTimer);
      };
    }

    requestTimer = window.setTimeout(() => {
      void request();
    }, 0);

    function handleVisibilityChange() {
      if (document.visibilityState === "visible") {
        void request();
      }
    }

    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      if (requestTimer !== null) window.clearTimeout(requestTimer);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      void release();
    };
  }, [enabled, release, request]);

  return { error, isActive, isSupported, release, request };
}
