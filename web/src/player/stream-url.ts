const preconnectedOrigins = new Set<string>();

/**
 * Warm the connection (DNS + TCP + TLS) to a stream origin as soon as it is
 * known. In distributed deployments the stream URL points at a proxy node the
 * browser has never contacted, and without this the first manifest request
 * pays all the handshakes after the transcode has already started.
 */
export function preconnectToStreamOrigin(streamUrl: string): void {
  if (!streamUrl.startsWith("http://") && !streamUrl.startsWith("https://")) return;
  let origin: string;
  try {
    origin = new URL(streamUrl).origin;
  } catch {
    return;
  }
  if (typeof document === "undefined" || origin === window.location.origin) return;
  if (preconnectedOrigins.has(origin)) return;
  preconnectedOrigins.add(origin);

  const link = document.createElement("link");
  link.rel = "preconnect";
  link.href = origin;
  // hls.js fetches are anonymous-mode CORS requests; the warmed connection
  // is only reused when the preconnect uses the same credentials mode.
  link.crossOrigin = "anonymous";
  document.head.appendChild(link);
}

/**
 * Makes a server-issued stream path loadable by a native media element, which
 * cannot set an Authorization header, by appending the access token as a query
 * parameter.
 *
 * Under protocol v3 the plan's `stream.url` is already fully anchored by the
 * server — the seek position, the stream token, and every other routing
 * decision are baked in. This helper must not add playback semantics of its
 * own; it only carries authentication.
 */
export function buildPlayerStreamUrl(
  apiBaseUrl: string,
  streamPath: string,
  token: string | null,
): string {
  // Versioned server paths are relative to the API installation root; the
  // player's configured base already includes the bridge API namespace.
  const apiRoot = /^\/api\/v[12]\//.test(streamPath)
    ? apiBaseUrl.replace(/\/api\/v[12]\/?$/, "")
    : apiBaseUrl;
  const base =
    streamPath.startsWith("http://") || streamPath.startsWith("https://")
      ? streamPath
      : `${apiRoot}${streamPath}`;
  if (!token) {
    return base;
  }
  const query = new URLSearchParams({ token }).toString();
  // The backend stream URL may already carry its own query string (e.g. the
  // `?st=<streamtoken>` reconstruct token for integrated-mode direct/remux).
  // Join with `&` in that case so we don't clobber it into `st=X?token=Y`.
  const separator = base.includes("?") ? "&" : "?";
  return `${base}${separator}${query}`;
}

export interface SubtitleRequest {
  url: string;
  headers?: Readonly<Record<string, string>>;
}

/** Resolve the exact proxy auxiliary resource before releasing captured credentials. */
export function proxySubtitleRequest(
  raw: string,
  stream: { url: string },
  capturedHeaders: Readonly<Record<string, string>>,
  sessionId: string,
  track: number,
  fileIds: readonly number[],
  apiBaseUrl: string,
  fonts = false,
): SubtitleRequest | null {
  try {
    if (!Number.isSafeInteger(track) || track < 0) return null;
    if (!/^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(sessionId)) return null;
    // Reject encoded path delimiters and normalization before URL parses them.
    if (
      [raw, stream.url].some(
        (value) =>
          /[%\\\s]/.test(value.split("?")[0] ?? "") ||
          value.includes("/../") ||
          value.includes("/./"),
      )
    )
      return null;
    const source = new URL(stream.url);
    const url = new URL(raw);
    const api = new URL(apiBaseUrl, window.location.href);
    if (
      [source, url].some(
        (value) =>
          !["http:", "https:"].includes(value.protocol) ||
          value.username ||
          value.password ||
          value.hash,
      )
    )
      return null;
    if (api.protocol === "https:" && source.protocol !== "https:") return null;
    if (
      url.origin !== source.origin ||
      !(
        [`/stream/v3/${sessionId}`, `/stream/v3/${sessionId}/master.m3u8`].includes(
          source.pathname,
        ) ||
        /^\/stream\/direct\/[A-Za-z0-9._-]+$/.test(source.pathname) ||
        /^\/stream\/transcode\/[A-Za-z0-9._-]+\/master\.m3u8$/.test(source.pathname)
      )
    )
      return null;
    if (
      [...source.searchParams].some(
        ([key, value]) => key !== "seek" || !/^(?:\d+)(?:\.\d+)?$/.test(value),
      ) ||
      source.searchParams.getAll("seek").length > 1
    )
      return null;
    const path = `/stream/v3/${sessionId}/subtitles/${track}`;
    const suffix = fonts ? "/fonts" : "";
    if (
      !["", ".ass", ".ssa", ".srt", ".vtt", ".sup"].some(
        (format) => url.pathname === path + format + suffix,
      )
    )
      return null;
    const names = new Set<string>();
    let identities = 0;
    for (const [key, value] of url.searchParams) {
      if (names.has(key) || !value) return null;
      names.add(key);
      if (key === "file_id") {
        if (!fileIds.some((id) => String(id) === value)) return null;
      } else if (key === "embedded_stream_index" || key === "downloaded_subtitle_id") {
        if (!/^(0|[1-9]\d*)$/.test(value)) return null;
        identities++;
      } else if (key === "external_subtitle_key") {
        if (!/^[\da-f]{64}$/i.test(value)) return null;
        identities++;
      } else if (["position", "duration"].includes(key)) {
        if (!/^\d+(?:\.\d+)?$/.test(value)) return null;
      } else if (key === "windowed") {
        if (!["true", "false", "1", "0"].includes(value)) return null;
      } else return null;
    }
    if (!names.has("file_id") || identities !== 1) return null;
    const headers: Record<string, string> = {};
    for (const [name, value] of Object.entries(capturedHeaders)) {
      const key = name.toLowerCase();
      if (!["authorization", "x-profile-id"].includes(key)) continue;
      if (headers[key] !== undefined || /[\r\n]/.test(value)) return null;
      headers[key] = value;
    }
    if (!/^Bearer \S+$/i.test(headers.authorization ?? "") || !headers["x-profile-id"]?.trim())
      return null;
    return { url: raw, headers: Object.freeze(headers) };
  } catch {
    return null;
  }
}

/** Scoped auxiliary requests never refresh ambient auth or follow redirects. */
export function subtitleFetchOptions(
  headers: Readonly<Record<string, string>> | undefined,
  signal?: AbortSignal,
  isCurrent?: () => boolean,
): RequestInit {
  if (isCurrent && !isCurrent()) throw new Error("Playback identity changed");
  return headers ? { signal, headers, redirect: "error", credentials: "omit" } : { signal };
}
