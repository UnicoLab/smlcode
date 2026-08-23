import '@testing-library/jest-dom/vitest';
import { afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  try {
    sessionStorage.clear();
    localStorage.clear();
  } catch {
    // jsdom always provides storage; guard only for exotic environments.
  }
});

// jsdom implements neither of these and several components call them.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

// ── EventSource test double ──
//
// jsdom has no EventSource. This one records instances so a test can drive the
// stream by hand: `MockEventSource.last.emitMessage(event, seq)`.

type Listener = (ev: MessageEvent) => void;

// Not `implements EventSource`: the DOM interface's addEventListener overloads
// (EventListenerObject, per-key event maps) are far wider than the handful of
// frames the stream hook uses, and matching them adds noise without adding
// safety. The hook only touches what is declared below.
export class MockEventSource {
  static instances: MockEventSource[] = [];

  static get last(): MockEventSource {
    const es = MockEventSource.instances[MockEventSource.instances.length - 1];
    if (!es) throw new Error('no EventSource was created');
    return es;
  }

  static reset() {
    MockEventSource.instances = [];
  }

  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  url: string;
  readyState = 0;
  onmessage: Listener | null = null;
  onerror: ((ev: Event) => void) | null = null;
  onopen: ((ev: Event) => void) | null = null;
  closed = false;

  private listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: Listener) {
    const list = this.listeners.get(type) ?? [];
    list.push(fn);
    this.listeners.set(type, list);
  }

  removeEventListener(type: string, fn: Listener) {
    this.listeners.set(type, (this.listeners.get(type) ?? []).filter((f) => f !== fn));
  }

  close() {
    this.closed = true;
    this.readyState = 2;
  }

  /** Simulate the server's named `connected` frame. */
  open(lastSeq = 0) {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
    this.dispatch('connected', JSON.stringify({ kind: 'connected', last_seq: lastSeq }));
  }

  /** Simulate an unnamed data frame with an SSE id. */
  emitMessage(data: unknown, seq: number) {
    const ev = new MessageEvent('message', {
      data: typeof data === 'string' ? data : JSON.stringify(data),
      lastEventId: String(seq),
    });
    this.onmessage?.(ev);
  }

  /** Simulate a named frame such as `gap`. */
  dispatch(type: string, data: string) {
    const ev = new MessageEvent(type, { data });
    (this.listeners.get(type) ?? []).forEach((fn) => fn(ev));
  }

  fail() {
    this.readyState = 2;
    this.onerror?.(new Event('error'));
  }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
(globalThis as any).EventSource = MockEventSource;
