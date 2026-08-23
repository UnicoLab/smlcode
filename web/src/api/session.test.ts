import { beforeEach, describe, expect, it } from 'vitest';
import { authHeaders, resetStudioToken, setStudioToken, studioToken, withToken, TOKEN_HEADER } from './session';

function setUrl(url: string) {
  window.history.replaceState(null, '', url);
}

describe('studio session token', () => {
  beforeEach(() => {
    resetStudioToken();
    sessionStorage.clear();
    document.head.innerHTML = '';
    setUrl('/');
  });

  it('reads the token from ?t= and strips it from the address bar', () => {
    setUrl('/?t=abc123');
    expect(studioToken()).toBe('abc123');
    // The token must not linger in history, bookmarks or screenshots.
    expect(window.location.search).not.toContain('t=');
  });

  it('preserves other query parameters when stripping the token', () => {
    setUrl('/board?t=abc123&filter=blocked');
    expect(studioToken()).toBe('abc123');
    expect(window.location.search).toContain('filter=blocked');
    expect(window.location.pathname).toBe('/board');
  });

  it('persists the token for the tab so a reload keeps working', () => {
    setUrl('/?t=abc123');
    studioToken();
    resetStudioToken();
    setUrl('/');
    expect(studioToken()).toBe('abc123');
  });

  it('falls back to the meta tag the server injects into index.html', () => {
    const meta = document.createElement('meta');
    meta.setAttribute('name', 'slmcode-token');
    meta.setAttribute('content', 'from-meta');
    document.head.appendChild(meta);
    expect(studioToken()).toBe('from-meta');
  });

  it('prefers the URL parameter over a stale stored token', () => {
    setStudioToken('stale');
    resetStudioToken();
    setUrl('/?t=fresh');
    expect(studioToken()).toBe('fresh');
  });

  it('returns no header when auth is disabled server-side', () => {
    expect(studioToken()).toBe('');
    expect(authHeaders()).toEqual({});
    expect(withToken('/api/events')).toBe('/api/events');
  });

  it('attaches the token as a header and as an EventSource query parameter', () => {
    setStudioToken('tok');
    expect(authHeaders()).toEqual({ [TOKEN_HEADER]: 'tok' });
    // EventSource cannot set headers, so the token must ride in the URL.
    expect(withToken('/api/events')).toBe('/api/events?t=tok');
    expect(withToken('/api/events?last_event_id=5')).toBe('/api/events?last_event_id=5&t=tok');
  });
});
