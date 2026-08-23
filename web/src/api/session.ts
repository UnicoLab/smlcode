// ── Studio session token ──
//
// Studio is a local agent that can read the repo, rewrite config, store API
// keys and start runs. The server therefore refuses non-loopback hosts, emits
// no permissive CORS headers, and (when started with a token) requires a
// session secret on every /api/* call.
//
// The SPA obtains that secret from, in order:
//   1. the `?t=` query parameter of the URL the CLI printed,
//   2. `sessionStorage` (survives in-tab navigation and reloads),
//   3. the `<meta name="slmcode-token">` tag the server injects into index.html
//      (so a tab opened at the bare URL still works).
//
// The parameter is stripped from the address bar immediately so the token does
// not linger in history, bookmarks or screenshots.

const STORAGE_KEY = 'slmcode:token';
const META_NAME = 'slmcode-token';
export const TOKEN_HEADER = 'X-SLMCode-Token';

let cached: string | null = null;

function readStorage(): string {
  try {
    return sessionStorage.getItem(STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

function writeStorage(token: string): void {
  try {
    sessionStorage.setItem(STORAGE_KEY, token);
  } catch {
    /* private mode — the in-memory cache still covers this tab */
  }
}

function readMeta(): string {
  if (typeof document === 'undefined') return '';
  const el = document.querySelector(`meta[name="${META_NAME}"]`);
  return el?.getAttribute('content')?.trim() || '';
}

function readQueryParam(): string {
  if (typeof window === 'undefined') return '';
  try {
    const url = new URL(window.location.href);
    const t = url.searchParams.get('t');
    if (!t) return '';
    url.searchParams.delete('t');
    const clean = url.pathname + (url.searchParams.toString() ? `?${url.searchParams}` : '') + url.hash;
    window.history.replaceState(null, '', clean);
    return t.trim();
  } catch {
    return '';
  }
}

/** Resolve the session token, caching the result for the tab. */
export function studioToken(): string {
  if (cached !== null) return cached;
  const fromQuery = readQueryParam();
  if (fromQuery) {
    writeStorage(fromQuery);
    cached = fromQuery;
    return cached;
  }
  const stored = readStorage();
  if (stored) {
    cached = stored;
    return cached;
  }
  const meta = readMeta();
  if (meta) {
    writeStorage(meta);
    cached = meta;
    return cached;
  }
  cached = '';
  return cached;
}

/** Overwrite the stored token (used by tests and by a manual re-auth flow). */
export function setStudioToken(token: string): void {
  cached = token;
  writeStorage(token);
}

/** Clear the cached token — next call re-reads the URL/meta/storage. */
export function resetStudioToken(): void {
  cached = null;
}

/** Headers to attach to an /api/* fetch. */
export function authHeaders(): Record<string, string> {
  const token = studioToken();
  return token ? { [TOKEN_HEADER]: token } : {};
}

/**
 * Append the token to a URL. Required for EventSource, which cannot set
 * request headers.
 */
export function withToken(url: string): string {
  const token = studioToken();
  if (!token) return url;
  return url + (url.includes('?') ? '&' : '?') + `t=${encodeURIComponent(token)}`;
}
