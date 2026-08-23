// ── Studio session token ──
//
// Studio is a local agent that can read the repo, rewrite config, store API
// keys and start runs. The server therefore refuses non-loopback hosts, emits
// no permissive CORS headers, and (when started with a token) requires a
// session secret on EVERY request — the HTML shell included.
//
// How the browser gets authenticated:
//
//   1. The CLI prints `http://127.0.0.1:7420/?t=<token>`. The server validates
//      `?t=` and replies with an HttpOnly, SameSite=Strict session cookie.
//   2. From then on the cookie authenticates everything: `fetch` sends it
//      (credentials: 'same-origin') and so does EventSource, which is
//      same-origin. Nothing in the page needs to hold the secret.
//
// The server no longer injects `<meta name="slmcode-token">` into index.html:
// that made `GET /` an unauthenticated token dispenser for any other process
// on the machine. There is no meta fallback here any more, by design.
//
// The token is still cached in memory + sessionStorage as a belt-and-braces
// fallback for a browser that refuses cookies, and is still stripped from the
// address bar immediately so it does not linger in history or screenshots.

const STORAGE_KEY = 'slmcode:token';
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

/**
 * Resolve the session token, caching the result for the tab.
 * Returns '' once the cookie has taken over — that is the normal steady state.
 */
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
  // No token in hand: the session cookie the server set during the `?t=`
  // bootstrap is what authenticates us. An empty string means "send nothing
  // extra", not "unauthenticated".
  cached = '';
  return cached;
}

/** Overwrite the stored token (used by tests and by a manual re-auth flow). */
export function setStudioToken(token: string): void {
  cached = token;
  writeStorage(token);
}

/** Clear the cached token — next call re-reads the URL / sessionStorage. */
export function resetStudioToken(): void {
  cached = null;
}

/**
 * Headers to attach to an /api/* fetch. Empty once the cookie authenticates
 * the tab; `fetch` is called with credentials: 'same-origin'.
 */
export function authHeaders(): Record<string, string> {
  const token = studioToken();
  return token ? { [TOKEN_HEADER]: token } : {};
}

/**
 * Append the token to a URL when we still hold one. EventSource cannot set
 * headers, but it is same-origin and therefore sends the session cookie, so
 * this is only the fallback for a cookie-less browser.
 */
export function withToken(url: string): string {
  const token = studioToken();
  if (!token) return url;
  return url + (url.includes('?') ? '&' : '?') + `t=${encodeURIComponent(token)}`;
}
