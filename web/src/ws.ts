export function wsUrl(path: "/source" | "/destination"): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

export const MIME = "audio/webm;codecs=opus";
