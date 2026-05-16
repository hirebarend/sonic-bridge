export function wsUrl(path: "/source" | "/destination"): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

export const RECORDER_MIME = "audio/mp4";
export const PLAYBACK_MIME = 'audio/mp4;codecs="mp4a.40.2"';
