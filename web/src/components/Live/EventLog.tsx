import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { RefObject } from 'react';
import type { RunEvent, RunEventSummary } from '@/types';
import EntityLink from '@/components/shared/EntityLink';
import clsx from 'clsx';

// ── The log, as rows ─────────────────────────────────────────────────────
//
// Two costs used to be paid on every stream flush, for every event in the
// log: describing it (describeEvent, with its regexes and JSON parsing) and
// summing it into the insight panel (summarizeEvents). Both are now paid
// once per event: descriptions are cached by event identity in a WeakMap —
// an event object never changes once it has arrived — and the summary is an
// accumulator that folds only the events appended since the last render.
// Rows are windowed to what is on screen plus a margin, so a two-thousand
// line log costs the DOM a few dozen rows, while the scroll container's
// stick-to-bottom keeps working because the spacers keep its height honest.

interface EventLogProps {
  events: RunEvent[];
  summary?: RunEventSummary | null;
  /**
   * The scroll container the rows live in, for windowing. Without it every
   * row renders (tests, a short archived log).
   */
  scrollRef?: RefObject<HTMLElement | null>;
}

/** Below this many rows nothing is windowed; the bookkeeping would cost more than it saves. */
const WINDOW_FROM = 120;
/** Rows kept rendered above and below the viewport. */
const WINDOW_MARGIN = 24;
/** A first guess at a row's height; measured rows correct it. */
const ROW_PX_GUESS = 76;

const PHASE_COLORS: Record<string, string> = {
  init: 'text-sky-500',
  skills: 'text-teal-500',
  context: 'text-cyan-500',
  explore: 'text-blue-500',
  docs: 'text-indigo-500',
  architect: 'text-violet-500',
  clarify: 'text-purple-500',
  plan: 'text-fuchsia-500',
  split: 'text-pink-500',
  coord: 'text-rose-500',
  execute: 'text-amber-500',
  learn: 'text-orange-500',
  polish: 'text-yellow-500',
  test: 'text-lime-500',
  memory: 'text-emerald-500',
  done: 'text-green-500',
  compose: 'text-teal-500',
  error: 'text-red-500',
};

const KIND_ICONS: Record<string, string> = {
  run_start: 'RUN',
  run_done: 'OK',
  run_error: 'ERR',
  agent_start: 'GO',
  agent_end: 'END',
  task_start: 'GO',
  task_done: 'OK',
  task_fail: 'ERR',
  review: 'REV',
  correct: 'FIX',
  coord: 'CO',
  plan: 'PLAN',
  explore: 'FIND',
  context: 'CTX',
  calibration: 'CAL',
  clarify: 'ASK',
  split: 'SPLIT',
  polish: 'POLISH',
  test: 'TEST',
  memory: 'MEM',
  output: 'OUT',
  tool: 'TOOL',
  file_change: 'FILE',
  shell: 'SH',
  wave: 'WAVE',
  gate: 'GATE',
  loop: 'LOOP',
  ask: 'ASK',
  intervention: 'HELP',
  latency: 'TIME',
  usage: 'TOK',
  debug: 'DBG',
  rewind: 'RW',
};

// Severity styling — problems/warnings/errors are visually distinct from routine info.
const LEVEL_STYLES: Record<string, { row: string; badge: string; label: string; icon: string }> = {
  error: { row: 'bg-red-50/70 dark:bg-red-900/15', badge: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300', label: 'ERROR', icon: 'ERR' },
  problem: { row: 'bg-orange-50/70 dark:bg-orange-900/15', badge: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300', label: 'PROBLEM', icon: '!' },
  warning: { row: 'bg-amber-50/60 dark:bg-amber-900/10', badge: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300', label: 'WARN', icon: '!' },
  success: { row: 'bg-green-50/60 dark:bg-green-900/10', badge: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300', label: 'OK', icon: 'OK' },
  info: { row: '', badge: 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400', label: 'INFO', icon: '·' },
};

type Filter = 'all' | 'problems';

type DisplayEvent = {
  event: RunEvent;
  count: number;
  signature: string;
  view: EventView;
};

type EventView = {
  icon: string;
  title: string;
  subtitle?: string;
  detail?: string;
  preview?: string;
  raw?: string;
  chips: { label: string; tone?: 'phase' | 'agent' | 'task' | 'file' | 'kind'; id?: string }[];
};

function EventLog({ events, summary, scrollRef }: EventLogProps) {
  const [filter, setFilter] = useState<Filter>('all');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const folded = useIncrementalSummary(events);
  const insightSummary = summary || folded;

  const counts = useMemo(() => {
    const c = { error: 0, problem: 0, warning: 0, success: 0 };
    for (const e of events) {
      const lvl = eventLevel(e);
      if (lvl in c) c[lvl as keyof typeof c] += 1;
    }
    return c;
  }, [events]);

  const visible = useMemo(() => {
    if (filter === 'all') return events;
    return events.filter((e) => {
      const lvl = eventLevel(e);
      return lvl === 'error' || lvl === 'problem' || lvl === 'warning';
    });
  }, [events, filter]);

  const displayEvents = useMemo(() => compactAdjacentEvents(visible), [visible]);
  const listRef = useRef<HTMLDivElement>(null);
  const win = useRowWindow(scrollRef, listRef, displayEvents.length);

  const toggleExpanded = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  if (events.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-xs text-gray-400">
        Waiting for events…
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <RunInsightPanel summary={insightSummary} />

      {/* Summary + filter bar */}
      <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-gray-50 dark:bg-gray-800/40 text-[10px]">
        <button
          onClick={() => setFilter('all')}
          className={clsx(
            'px-2 py-1 rounded-md font-medium transition-colors',
            filter === 'all'
              ? 'bg-gray-200 text-gray-800 dark:bg-gray-700 dark:text-gray-100'
              : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300',
          )}
        >
          All ({events.length})
        </button>
        <button
          onClick={() => setFilter('problems')}
          className={clsx(
            'px-2 py-1 rounded-md font-medium transition-colors',
            filter === 'problems'
              ? 'bg-gray-200 text-gray-800 dark:bg-gray-700 dark:text-gray-100'
              : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300',
          )}
        >
          Problems ({counts.error + counts.problem + counts.warning})
        </button>
        <span className="ml-auto flex items-center gap-2 text-gray-400 dark:text-gray-500">
          {counts.error > 0 && <span className="text-red-500">ERR {counts.error}</span>}
          {counts.problem > 0 && <span className="text-orange-500">PROBLEM {counts.problem}</span>}
          {counts.warning > 0 && <span className="text-amber-500">WARN {counts.warning}</span>}
          {counts.success > 0 && <span className="text-green-500">OK {counts.success}</span>}
        </span>
      </div>

      <div ref={listRef} className="space-y-0.5 font-mono text-xs" data-testid="event-log-rows" data-window={`${win.start}-${win.end}`}>
        {win.start > 0 && <div style={{ height: win.top }} aria-hidden="true" />}
        {displayEvents.slice(win.start, win.end).map((item, k) => {
          const i = win.start + k;
          const event = item.event;
          const view = item.view;
          const rowKey = `${item.signature}-${event.time}-${i}`;
          const lvl = eventLevel(event);
          const style = LEVEL_STYLES[lvl] || LEVEL_STYLES.info;
          const isExpanded = expanded.has(rowKey);
          return (
            <div
              key={rowKey}
              data-row
              className={clsx(
                'rounded-lg px-3 py-2 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800/50',
                style.row,
                event.phase === 'error' && 'bg-red-50/50 dark:bg-red-900/10',
              )}
            >
              <div className="flex items-start gap-3">
                <span
                  className={clsx(
                    'mt-0.5 flex h-6 w-10 shrink-0 items-center justify-center rounded-md border font-sans text-[9px] font-bold tracking-wide',
                    lvl === 'error' && 'border-red-200 bg-red-100 text-red-700 dark:border-red-900 dark:bg-red-950/50 dark:text-red-300',
                    lvl === 'problem' && 'border-orange-200 bg-orange-100 text-orange-700 dark:border-orange-900 dark:bg-orange-950/50 dark:text-orange-300',
                    lvl === 'warning' && 'border-amber-200 bg-amber-100 text-amber-700 dark:border-amber-900 dark:bg-amber-950/50 dark:text-amber-300',
                    lvl === 'success' && 'border-green-200 bg-green-100 text-green-700 dark:border-green-900 dark:bg-green-950/50 dark:text-green-300',
                    lvl === 'info' && 'border-gray-200 bg-gray-100 text-gray-500 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400',
                  )}
                  title={`${style.label} / ${event.kind}`}
                >
                  {lvl !== 'info' ? style.icon : view.icon}
                </span>

                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="font-sans text-[10px] text-gray-400 dark:text-gray-600 tabular-nums">
                      {formatTime(event.time)}
                    </span>
                    <span className={clsx('font-sans text-[10px] font-semibold uppercase tracking-wider', PHASE_COLORS[event.phase] || 'text-gray-500')}>
                      {event.phase || 'event'}
                    </span>
                    {item.count > 1 && (
                      <span className="rounded bg-gray-100 px-1.5 py-0.5 font-sans text-[9px] font-semibold text-gray-500 dark:bg-gray-800 dark:text-gray-400">
                        repeated x{item.count}
                      </span>
                    )}
                    {view.chips.map((chip, idx) =>
                      // An agent, a task or a file is a place in the studio;
                      // the chip goes there. A kind or a phase is just a word.
                      chip.tone === 'agent' || chip.tone === 'task' || chip.tone === 'file' ? (
                        <EntityLink
                          key={`${chip.label}-${idx}`}
                          kind={chip.tone}
                          id={chip.id ?? chip.label}
                          label={chip.label}
                          className="text-[9px]"
                          bare
                        />
                      ) : (
                        <span
                          key={`${chip.label}-${idx}`}
                          className={clsx(
                            'rounded px-1.5 py-0.5 font-sans text-[9px] font-semibold',
                            chip.tone === 'kind' && 'bg-sky-50 text-sky-600 dark:bg-sky-950/40 dark:text-sky-300',
                            (!chip.tone || chip.tone === 'phase') && 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400',
                          )}
                        >
                          {chip.label}
                        </span>
                      ),
                    )}
                  </div>

                  <div className="mt-1 font-sans text-sm font-semibold leading-snug text-gray-800 dark:text-gray-100">
                    {view.title}
                  </div>
                  {view.subtitle && (
                    <div className="mt-0.5 font-sans text-xs leading-snug text-gray-500 dark:text-gray-400">
                      {view.subtitle}
                    </div>
                  )}
                  {view.detail && (
                    <div className="mt-1 rounded-md bg-gray-50 px-2 py-1.5 font-sans text-xs leading-relaxed text-gray-600 dark:bg-gray-900/70 dark:text-gray-300">
                      {view.detail}
                    </div>
                  )}
                  {view.preview && (
                    <button
                      type="button"
                      onClick={() => toggleExpanded(rowKey)}
                      className="mt-1 block w-full rounded-md border border-gray-200 bg-white px-2 py-1.5 text-left font-mono text-[10px] leading-relaxed text-gray-500 hover:border-gray-300 dark:border-gray-800 dark:bg-gray-950 dark:text-gray-400 dark:hover:border-gray-700"
                      title={isExpanded ? 'Collapse event output' : 'Expand event output'}
                    >
                      <span className={clsx('block whitespace-pre-wrap break-words', !isExpanded && 'line-clamp-2')}>
                        {isExpanded ? view.raw || view.preview : view.preview}
                      </span>
                    </button>
                  )}
                </div>
              </div>
            </div>
          );
        })}
        {win.end < displayEvents.length && <div style={{ height: win.bottom }} aria-hidden="true" />}
      </div>
    </div>
  );
}

// ── Windowing ────────────────────────────────────────────────────────────

interface RowWindow {
  start: number;
  end: number;
  /** Spacer heights standing in for the rows not rendered, px. */
  top: number;
  bottom: number;
}

/**
 * useRowWindow picks the slice of rows worth rendering: the ones the scroll
 * container can show, plus WINDOW_MARGIN either side. Row height starts as a
 * guess and is corrected from what actually renders. When the container is
 * at its bottom as rows are appended, the window jumps to the new tail at
 * once — in a layout effect, before paint — so the stick-to-bottom scroll
 * that follows lands on real rows rather than on a spacer.
 */
function useRowWindow(scrollRef: RefObject<HTMLElement | null> | undefined, listRef: RefObject<HTMLDivElement | null>, count: number): RowWindow {
  const active = !!scrollRef && count >= WINDOW_FROM;
  const rowPx = useRef(ROW_PX_GUESS);
  const atBottom = useRef(true);
  const [range, setRange] = useState<{ start: number; end: number }>({ start: 0, end: count });

  const measure = (): { start: number; end: number } | null => {
    const el = scrollRef?.current;
    const list = listRef.current;
    if (!el || !list) return null;
    const listTop = list.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop;
    const px = rowPx.current;
    const first = Math.floor((el.scrollTop - listTop) / px);
    const last = Math.ceil((el.scrollTop - listTop + el.clientHeight) / px);
    return {
      start: Math.max(0, Math.min(count, first - WINDOW_MARGIN)),
      end: Math.max(0, Math.min(count, last + WINDOW_MARGIN)),
    };
  };
  const measureRef = useRef(measure);
  measureRef.current = measure;

  // Correct the row estimate from the rows that did render.
  useLayoutEffect(() => {
    if (!active) return;
    const list = listRef.current;
    if (!list) return;
    const rows = list.querySelectorAll<HTMLElement>('[data-row]');
    if (rows.length < 4) return;
    let sum = 0;
    rows.forEach((r) => {
      sum += r.offsetHeight + 2;
    });
    const avg = sum / rows.length;
    if (avg > 8 && Math.abs(avg - rowPx.current) / rowPx.current > 0.1) rowPx.current = avg;
  });

  // Scroll and resize move the window; both are cheap since the range only
  // changes state when its bounds do.
  useEffect(() => {
    const el = scrollRef?.current;
    if (!active || !el) return undefined;
    const apply = () => {
      atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight <= 72;
      const next = measureRef.current();
      if (!next) return;
      setRange((prev) => (prev.start === next.start && prev.end === next.end ? prev : next));
    };
    apply();
    el.addEventListener('scroll', apply, { passive: true });
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(apply) : null;
    ro?.observe(el);
    return () => {
      el.removeEventListener('scroll', apply);
      ro?.disconnect();
    };
  }, [active, scrollRef]);

  // New rows: keep the tail in view when the reader is at the bottom.
  useLayoutEffect(() => {
    if (!active) return;
    const el = scrollRef?.current;
    if (!el) return;
    if (atBottom.current) {
      const visibleRows = Math.ceil(el.clientHeight / rowPx.current);
      const next = { start: Math.max(0, count - visibleRows - WINDOW_MARGIN * 2), end: count };
      setRange((prev) => (prev.start === next.start && prev.end === next.end ? prev : next));
    } else {
      const next = measureRef.current();
      if (next) setRange((prev) => (prev.start === next.start && prev.end === next.end ? prev : next));
    }
  }, [active, count, scrollRef]);

  if (!active) return { start: 0, end: count, top: 0, bottom: 0 };
  const start = Math.min(range.start, count);
  const end = Math.min(Math.max(range.end, start), count);
  return { start, end, top: start * rowPx.current, bottom: (count - end) * rowPx.current };
}

// ── Per-event work, done once ────────────────────────────────────────────

const VIEW_CACHE = new WeakMap<RunEvent, EventView>();
const LEVEL_CACHE = new WeakMap<RunEvent, string>();
const SIGNATURE_CACHE = new WeakMap<RunEvent, string>();

function compactAdjacentEvents(events: RunEvent[]): DisplayEvent[] {
  const out: DisplayEvent[] = [];
  for (const event of events) {
    const signature = eventSignature(event);
    const last = out[out.length - 1];
    if (last && last.signature === signature) {
      last.count += 1;
      last.event = event;
      continue;
    }
    out.push({ event, count: 1, signature, view: describeEvent(event) });
  }
  return out;
}

/** The level of an event, computed once per event object. */
function eventLevel(event: RunEvent): string {
  const hit = LEVEL_CACHE.get(event);
  if (hit !== undefined) return hit;
  const lvl = computeEventLevel(event);
  LEVEL_CACHE.set(event, lvl);
  return lvl;
}

function computeEventLevel(event: RunEvent) {
  const configured = event.level || 'info';
  if (configured !== 'info') return configured;
  const text = `${event.phase || ''} ${event.kind || ''} ${event.message || ''} ${event.output || ''}`.toLowerCase();
  if (event.phase === 'error' || text.includes('context canceled') || text.includes('context cancelled') ||
    text.includes('deadline exceeded') || text.includes('timed out') || text.includes('panic') ||
    text.includes('exception')) {
    return 'error';
  }
  if (text.includes('failed') || text.includes('blocked') || text.includes('rejected') ||
    text.includes('still red') || text.includes('qa_gate failed')) {
    return 'problem';
  }
  if (text.includes('warning') || text.includes('warn') || text.includes('degraded')) {
    return 'warning';
  }
  if (text.includes('green') || text.includes('approved=true') || text.includes('passed') ||
    text.includes('run completed')) {
    return 'success';
  }
  return configured;
}

function eventSignature(event: RunEvent): string {
  const hit = SIGNATURE_CACHE.get(event);
  if (hit !== undefined) return hit;
  const sig = computeSignature(event);
  SIGNATURE_CACHE.set(event, sig);
  return sig;
}

function computeSignature(event: RunEvent) {
  return [
    event.phase || '',
    event.kind || '',
    event.level || '',
    event.agent || '',
    event.task_id || '',
    event.scope || '',
    normalizeLogText(event.message || ''),
    normalizeLogText(event.output || '').slice(0, 240),
  ].join('|');
}

/** The row's description, computed once per event object. */
function describeEvent(event: RunEvent): EventView {
  const hit = VIEW_CACHE.get(event);
  if (hit) return hit;
  const view = computeView(event);
  VIEW_CACHE.set(event, view);
  return view;
}

function computeView(event: RunEvent): EventView {
  const msg = cleanEventText(event.message || '');
  const output = cleanEventText(event.output || '');
  const lower = `${event.phase || ''} ${event.kind || ''} ${msg} ${output}`.toLowerCase();
  const actor = actorLabel(event);
  const file = primaryFile(event);
  const chips = eventChips(event, file);
  const preview = outputPreview(event);
  const base: EventView = {
    icon: KIND_ICONS[event.kind] || KIND_ICONS[event.phase] || 'EVT',
    title: msg || `${titleCase(event.kind || event.phase || 'event')} update`,
    preview,
    raw: output || undefined,
    chips,
  };

  if (event.phase === 'error' || lower.includes('context canceled') || lower.includes('context cancelled')) {
    return {
      ...base,
      icon: 'STOP',
      title: 'Run was interrupted or canceled',
      subtitle: 'The harness stopped before the pipeline reached a clean final state.',
      detail: msg || output || 'Context was canceled.',
    };
  }

  if (lower.includes('timed out') || lower.includes('deadline exceeded') || lower.includes('timeout')) {
    return {
      ...base,
      icon: 'TIME',
      title: `${actor} hit a timeout`,
      subtitle: file ? `The task on ${file} needs retry or narrower scope.` : 'The task needs retry or narrower scope.',
      detail: msg || output,
    };
  }

  if (event.kind === 'file_change') {
    const op = fileOperation(msg);
    return {
      ...base,
      icon: 'FILE',
      title: `${actor} ${op.verb} ${op.file || file || 'a file'}`,
      subtitle: op.explain,
      detail: file ? `Focus file: ${file}` : undefined,
    };
  }

  if (event.kind === 'agent_start') {
    return {
      ...base,
      icon: 'GO',
      title: `${actor} started${event.task_id ? ` task ${event.task_id}` : ''}`,
      subtitle: startSubtitle(event, msg),
      detail: file ? `Scope: ${file}` : undefined,
    };
  }

  if (event.kind === 'agent_end') {
    return {
      ...base,
      icon: endIcon(lower),
      title: endTitle(event, actor, msg),
      subtitle: endSubtitle(event, msg),
      detail: output && output !== msg ? summarizePayload(output, 220) : undefined,
    };
  }

  if (event.kind === 'turn') {
    return {
      ...base,
      icon: 'TURN',
      title: `${actor} progress update`,
      subtitle: msg,
    };
  }

  if (event.kind === 'loop') {
    const loop = parseLoopPayload(output);
    return {
      ...base,
      icon: 'LOOP',
      title: loop?.action ? loopActionTitle(loop.action) : 'Pipeline loop decision',
      subtitle: loop?.reason || msg,
      detail: loopDetail(loop),
    };
  }

  if (event.kind === 'ask') {
    return {
      ...base,
      icon: 'ASK',
      title: askTitle(event),
      subtitle: msg,
      detail: output ? summarizePayload(output, 260) : undefined,
    };
  }

  if (event.kind === 'intervention') {
    return {
      ...base,
      icon: 'HELP',
      title: interventionTitle(event),
      subtitle: msg,
      detail: output || undefined,
    };
  }

  if (event.kind === 'latency') {
    return {
      ...base,
      icon: 'TIME',
      title: 'Timing update',
      subtitle: msg,
    };
  }

  if (event.kind === 'usage') {
    return {
      ...base,
      icon: 'TOK',
      title: 'Token usage update',
      subtitle: msg,
    };
  }

  if (event.phase === 'test' || event.agent === 'qa' || lower.includes('qa_gate')) {
    return {
      ...base,
      icon: lower.includes('green') || lower.includes('passed') ? 'OK' : 'TEST',
      title: qaTitle(event, msg, lower),
      subtitle: output ? summarizePayload(output, 180) : msg,
    };
  }

  if (event.phase === 'learn' || event.phase === 'memory') {
    return {
      ...base,
      icon: 'MEM',
      title: 'Harness updated memory',
      subtitle: msg,
      detail: output ? summarizePayload(output, 220) : undefined,
    };
  }

  if (event.kind === 'output' && output) {
    return {
      ...base,
      title: `${actor} produced output`,
      subtitle: msg || summarizePayload(output, 140),
      detail: summarizePayload(output, 220),
    };
  }

  return {
    ...base,
    title: phaseTitle(event, msg),
    subtitle: defaultSubtitle(event),
    detail: output ? summarizePayload(output, 220) : undefined,
  };
}

function actorLabel(event: RunEvent) {
  const agent = cleanEventText(event.agent || '');
  if (!agent) return titleCase(event.phase || 'Harness');
  if (agent === 'qa') return 'QA gate';
  if (agent === 'loop') return 'Pipeline loop';
  if (agent === 'harness') return 'Harness';
  return titleCase(agent.replace(/[-_]/g, ' '));
}

function eventChips(event: RunEvent, file?: string) {
  const chips: EventView['chips'] = [];
  if (event.agent) chips.push({ label: `@${event.agent}`, tone: 'agent', id: event.agent });
  if (event.task_id) chips.push({ label: `#${event.task_id}`, tone: 'task', id: event.task_id });
  if (file) chips.push({ label: file, tone: 'file', id: file });
  if (event.kind && event.kind !== 'phase') chips.push({ label: event.kind, tone: 'kind' });
  return chips.slice(0, 5);
}

function primaryFile(event: RunEvent) {
  const scope = cleanEventText(event.scope || '');
  const msg = cleanEventText(event.message || '');
  const candidates = [scope, msg];
  for (const text of candidates) {
    const match = text.match(/[A-Za-z0-9_./-]+\.(go|ts|tsx|js|jsx|py|rs|java|cpp|c|h|hpp|css|html|md|yaml|yml|json|toml)/);
    if (match) return match[0];
  }
  return scope && scope.length < 80 ? scope : '';
}

function fileOperation(message: string) {
  const parts = message.trim().split(/\s+/);
  const op = (parts[0] || '').toLowerCase();
  const file = parts[1] || '';
  switch (op) {
    case 'write':
      return { verb: 'wrote', file, explain: 'Created or replaced file content through the workspace tool.' };
    case 'edit':
      return { verb: 'edited', file, explain: 'Applied a targeted patch to an existing file.' };
    case 'patch':
      return { verb: 'patched', file, explain: 'Applied a structured patch.' };
    case 'read':
      return { verb: 'read', file, explain: 'Loaded file context before deciding the next edit.' };
    case 'delete':
    case 'remove':
      return { verb: 'removed', file, explain: 'Deleted a file or block.' };
    case 'mv':
    case 'move':
    case 'rename':
      return { verb: 'renamed', file, explain: 'Moved or renamed a file.' };
    default:
      return { verb: 'updated', file, explain: message || 'File activity from the workspace tool.' };
  }
}

function startSubtitle(event: RunEvent, message: string) {
  if (message === 'correction pass') return 'Review found issues; the corrector is applying a fix.';
  if (message.includes('review')) return 'Checking whether the task is actually complete.';
  if (message.includes('worker self-critique')) return 'The harness detected weak output and is asking for a self-fix.';
  if (event.phase === 'test') return 'Verification is running against the current workspace.';
  return message || 'Agent call is in progress.';
}

function endTitle(event: RunEvent, actor: string, message: string) {
  const lower = message.toLowerCase();
  if (lower.includes('review approved')) return `${actor} approved the task`;
  if (lower.includes('review approved=false')) return `${actor} rejected the task`;
  if (lower.includes('corrector finished')) return `${actor} finished a correction`;
  if (lower.includes('worker finished')) return `${actor} finished task work`;
  if (lower.includes('timed out')) return `${actor} timed out`;
  if (lower.includes('error')) return `${actor} ended with an error`;
  if (lower.includes('green')) return `${actor} passed`;
  return `${actor} finished`;
}

function endSubtitle(event: RunEvent, message: string) {
  const lower = message.toLowerCase();
  if (lower.includes('review approved=true')) return 'The reviewer accepted the implementation for this task.';
  if (lower.includes('review approved=false')) return 'The task will move into correction or escalation.';
  if (lower.includes('corrector')) return 'The task should return to review after this fix.';
  if (event.task_id) return `Task ${event.task_id} moved forward in the pipeline.`;
  return message;
}

function endIcon(lower: string) {
  if (lower.includes('error') || lower.includes('false') || lower.includes('red')) return 'ERR';
  if (lower.includes('approved=true') || lower.includes('green') || lower.includes('passed')) return 'OK';
  return 'END';
}

function parseLoopPayload(output: string): { action?: string; reason?: string; from?: string; to?: string; awaiting?: boolean; failures?: string[] } | null {
  try {
    const raw = extractJSON(output);
    if (!raw) return null;
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function loopActionTitle(action: string) {
  const label = action.replace(/_/g, ' ');
  if (action.includes('corrective') || action.includes('continue')) return `Running another ${label}`;
  if (action.includes('pending')) return `Waiting for ${label.replace(' pending', '')}`;
  if (action.includes('resolved')) return 'Loop resolved';
  if (action.includes('rewrite')) return 'Rewriting tasks from feedback';
  return titleCase(label);
}

function loopDetail(loop: ReturnType<typeof parseLoopPayload>) {
  if (!loop) return undefined;
  const parts = [];
  if (loop.from || loop.to) parts.push(`${loop.from || '?'} -> ${loop.to || '?'}`);
  if (loop.awaiting) parts.push('waiting for user or timeout');
  if (loop.failures && loop.failures.length) parts.push(`failures: ${loop.failures.slice(0, 3).join('; ')}`);
  return parts.join(' · ') || undefined;
}

function askTitle(event: RunEvent) {
  if (event.agent === 'continue') return 'Harness needs a continue decision';
  if (event.agent === 'escalate') return 'Task needs a retry/re-scope decision';
  if (event.agent === 'plan-approve') return 'Plan approval is waiting';
  if (event.agent === 'shell') return 'Shell command approval is waiting';
  return 'Harness is waiting for input';
}

function interventionTitle(event: RunEvent) {
  const scope = event.scope || '';
  if (scope === 'timeout') return 'Harness caught a timeout and made it actionable';
  if (scope === 'escalate') return 'Harness escalated a task for decision';
  if (scope === 'review') return 'Harness blocked a weak approval';
  if (scope === 'finalize') return 'Harness asked the agent to finish cleanly';
  if (scope === 'thinking_budget') return 'Harness stopped over-thinking';
  return 'Harness intervention';
}

function qaTitle(event: RunEvent, message: string, lower: string) {
  if (lower.includes('green') || lower.includes('passed')) return 'Verification passed';
  if (lower.includes('failed') || lower.includes('red')) return 'Verification failed';
  if (event.agent === 'qa') return 'QA gate update';
  return message || 'Verification update';
}

function phaseTitle(event: RunEvent, message: string) {
  if (event.kind === 'phase') return `${titleCase(event.phase || 'Pipeline')} phase: ${message}`;
  if (event.agent) return `${actorLabel(event)}: ${message}`;
  return message || `${titleCase(event.phase || event.kind || 'Pipeline')} update`;
}

function defaultSubtitle(event: RunEvent) {
  if (event.scope) return `Scope: ${event.scope}`;
  if (event.kind) return `Event kind: ${event.kind}`;
  return undefined;
}

function outputPreview(event: RunEvent) {
  const out = cleanEventText(event.output || '');
  if (!out) return undefined;
  const summarized = summarizePayload(out, 520);
  if (summarized === cleanEventText(event.message || '')) return undefined;
  return summarized;
}

function summarizePayload(raw: string, limit: number) {
  const text = cleanEventText(raw);
  if (!text) return '';
  const parsed = parseStatusJSON(text);
  if (parsed) return parsed;
  if (text.includes('\n')) {
    const lines = text.split('\n').map((line) => line.trim()).filter(Boolean);
    const interesting = lines.filter((line) => !line.startsWith('//') || line.length < 120).slice(0, 6);
    return truncate((interesting.length ? interesting : lines).join('\n'), limit);
  }
  return truncate(text, limit);
}

function parseStatusJSON(raw: string) {
  try {
    const json = extractJSON(raw);
    if (!json) return '';
    const obj = JSON.parse(json);
    const parts = [];
    if (obj.status) parts.push(`status: ${obj.status}`);
    if (typeof obj.passed === 'boolean') parts.push(`passed: ${obj.passed}`);
    if (obj.summary) parts.push(`summary: ${obj.summary}`);
    if (Array.isArray(obj.files_changed) && obj.files_changed.length) parts.push(`files: ${obj.files_changed.slice(0, 4).join(', ')}`);
    if (Array.isArray(obj.failures) && obj.failures.length) parts.push(`failures: ${obj.failures.slice(0, 3).join('; ')}`);
    if (Array.isArray(obj.commands) && obj.commands.length) parts.push(`commands: ${obj.commands.slice(0, 3).join('; ')}`);
    return parts.join('\n');
  } catch {
    return '';
  }
}

function extractJSON(raw: string) {
  const text = raw.trim();
  const objStart = text.indexOf('{');
  const objEnd = text.lastIndexOf('}');
  if (objStart >= 0 && objEnd > objStart) return text.slice(objStart, objEnd + 1);
  return '';
}

function cleanEventText(value: string) {
  return String(value || '').replace(/\s+/g, ' ').trim();
}

function normalizeLogText(value: string) {
  return cleanEventText(value).toLowerCase();
}

function titleCase(value: string) {
  return value
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}

function RunInsightPanel({ summary }: { summary: RunEventSummary }) {
  const insights = summary.insights || [];
  const actions = summary.actions || [];
  return (
    <div className="rounded-lg border border-gray-200 bg-white/70 p-3 dark:border-gray-800 dark:bg-gray-900/50">
      <div className="grid gap-2 sm:grid-cols-3 lg:grid-cols-6">
        <Metric label="Elapsed" value={formatDuration(summary.duration_ms)} />
        <Metric label="Tasks" value={String(summary.tasks || 0)} />
        <Metric label="Retries" value={String(summary.retries || 0)} tone={summary.retries >= 3 ? 'warning' : 'neutral'} />
        <Metric label="Replans" value={String(summary.replans || 0)} tone={summary.replans > 0 ? 'info' : 'neutral'} />
        <Metric label="Failures" value={String(summary.failures || 0)} tone={summary.failures > 0 ? 'error' : 'neutral'} />
        <Metric label="Final" value={summary.final_phase || 'pending'} tone={summary.final_phase === 'error' ? 'error' : 'neutral'} />
      </div>

      <div className="mt-3 grid gap-3 lg:grid-cols-[1.2fr_1fr]">
        <div className="space-y-1.5">
          <div className="text-[10px] font-semibold uppercase text-gray-400">Run Insights</div>
          {insights.length ? (
            insights.slice(0, 5).map((insight, i) => (
              <div
                key={`${insight.title}-${i}`}
                className={clsx(
                  'rounded-md border px-2.5 py-2 text-xs',
                  insight.severity === 'error' && 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300',
                  insight.severity === 'warning' && 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-300',
                  insight.severity !== 'error' && insight.severity !== 'warning' && 'border-gray-200 bg-gray-50 text-gray-600 dark:border-gray-800 dark:bg-gray-800/50 dark:text-gray-300',
                )}
              >
                <div className="flex items-center gap-2">
                  <span className="font-semibold">{insight.title}</span>
                  {insight.phase && <span className="ml-auto font-mono text-[10px] opacity-70">{insight.phase}</span>}
                </div>
                {insight.detail && <div className="mt-1 opacity-80">{insight.detail}</div>}
                {(insight.task_id || insight.agent) && (
                  <div className="mt-1 font-mono text-[10px] opacity-60">
                    {insight.task_id && `#${insight.task_id}`}
                    {insight.task_id && insight.agent && ' · '}
                    {insight.agent && `@${insight.agent}`}
                  </div>
                )}
              </div>
            ))
          ) : (
            <div className="rounded-md border border-emerald-200 bg-emerald-50 px-2.5 py-2 text-xs text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300">
              No retry, replan, or failure pressure detected in this event window.
            </div>
          )}
        </div>

        <div className="space-y-2">
          <TopList label="Agents" values={summary.agents || []} empty="No agent events yet" />
          <TopList label="Models" values={summary.models || []} empty="No model attribution yet" />
          {(summary.tokens || summary.cost_usd) && (
            <div className="grid grid-cols-2 gap-2">
              <Metric label="Tokens" value={summary.tokens ? String(summary.tokens) : '0'} />
              <Metric label="Cost" value={formatCost(summary.cost_usd)} />
            </div>
          )}
        </div>
      </div>

      {actions.length > 0 && (
        <div className="mt-3">
          <div className="text-[10px] font-semibold uppercase text-gray-400">Next Actions</div>
          <div className="mt-1.5 grid gap-1.5 md:grid-cols-2">
            {actions.slice(0, 4).map((action, i) => (
              <div key={`${action.title}-${i}`} className="rounded-md border border-sky-200 bg-sky-50 px-2.5 py-2 text-xs text-sky-800 dark:border-sky-900 dark:bg-sky-950/30 dark:text-sky-300">
                <div className="font-semibold">{action.title}</div>
                {action.detail && <div className="mt-1 text-sky-700/80 dark:text-sky-300/80">{action.detail}</div>}
                {action.command && (
                  <code className="mt-1 block truncate rounded bg-white/70 px-1.5 py-1 font-mono text-[10px] text-sky-900 dark:bg-gray-950/60 dark:text-sky-200">
                    {action.command}
                  </code>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Metric({
  label,
  value,
  tone = 'neutral',
}: {
  label: string;
  value: string;
  tone?: 'neutral' | 'warning' | 'error' | 'info';
}) {
  return (
    <div className="rounded-md bg-gray-50 px-2.5 py-2 dark:bg-gray-800/60">
      <div className="text-[10px] uppercase text-gray-400">{label}</div>
      <div
        className={clsx(
          'mt-1 truncate font-mono text-sm font-bold',
          tone === 'error' && 'text-red-600 dark:text-red-300',
          tone === 'warning' && 'text-amber-600 dark:text-amber-300',
          tone === 'info' && 'text-sky-600 dark:text-sky-300',
          tone === 'neutral' && 'text-gray-900 dark:text-gray-100',
        )}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}

function TopList({ label, values, empty }: { label: string; values: { name: string; count: number }[]; empty: string }) {
  return (
    <div>
      <div className="text-[10px] font-semibold uppercase text-gray-400">{label}</div>
      <div className="mt-1 flex flex-wrap gap-1.5">
        {values.slice(0, 5).map((v) => (
          <span key={v.name} className="rounded-md bg-gray-100 px-2 py-1 font-mono text-[10px] text-gray-600 dark:bg-gray-800 dark:text-gray-300">
            {v.name} <span className="text-gray-400">x{v.count}</span>
          </span>
        ))}
        {!values.length && <span className="text-[10px] text-gray-400">{empty}</span>}
      </div>
    </div>
  );
}

/** The running totals behind the insight panel. Folded per event; finished per render. */
interface SummaryAcc {
  count: number;
  phases: Map<string, number>;
  agents: Map<string, number>;
  models: Map<string, number>;
  tasks: Set<string>;
  retries: number;
  replans: number;
  failures: number;
  warnings: number;
  errors: number;
  toolCalls: number;
  shellCalls: number;
  tokens: number;
  costUSD: number;
  first: number;
  last: number;
  failureInsights: NonNullable<RunEventSummary['insights']>;
  providerIssue: boolean;
  modelIssue: boolean;
  qaIssue: boolean;
  contextIssue: boolean;
  permissionIssue: boolean;
  terminalSuccess: boolean;
  final: RunEvent | null;
}

function newSummaryAcc(): SummaryAcc {
  return {
    count: 0,
    phases: new Map(),
    agents: new Map(),
    models: new Map(),
    tasks: new Set(),
    retries: 0,
    replans: 0,
    failures: 0,
    warnings: 0,
    errors: 0,
    toolCalls: 0,
    shellCalls: 0,
    tokens: 0,
    costUSD: 0,
    first: 0,
    last: 0,
    failureInsights: [],
    providerIssue: false,
    modelIssue: false,
    qaIssue: false,
    contextIssue: false,
    permissionIssue: false,
    terminalSuccess: false,
    final: null,
  };
}

function foldSummary(acc: SummaryAcc, event: RunEvent): void {
  acc.count += 1;
  acc.final = event;
  if (event.phase) acc.phases.set(event.phase, (acc.phases.get(event.phase) || 0) + 1);
  if (event.agent) acc.agents.set(event.agent, (acc.agents.get(event.agent) || 0) + 1);
  if (event.model) acc.models.set(event.model, (acc.models.get(event.model) || 0) + 1);
  if (event.task_id) acc.tasks.add(event.task_id);
  if (typeof event.tokens === 'number' && event.tokens > 0) acc.tokens += event.tokens;
  if (typeof event.cost_usd === 'number' && event.cost_usd > 0) acc.costUSD += event.cost_usd;
  const t = Date.parse(event.time || '');
  if (!Number.isNaN(t)) {
    if (!acc.first) acc.first = t;
    acc.last = t;
  }
  const text = `${event.phase || ''} ${event.kind || ''} ${event.message || ''} ${event.output || ''}`.toLowerCase();
  if (text.includes('retry') || text.includes('corrective')) acc.retries += 1;
  if (text.includes('replan') || text.includes('plan was revised')) acc.replans += 1;
  if (text.includes('warn') || text.includes('degraded')) acc.warnings += 1;
  if (text.includes('error') || text.includes('panic') || text.includes('exception')) acc.errors += 1;
  if ((event.kind || '').includes('tool')) acc.toolCalls += 1;
  if ((event.kind || '').includes('shell') || text.includes('shell')) acc.shellCalls += 1;
  acc.providerIssue ||= looksLikeProviderIssue(text);
  acc.modelIssue ||= looksLikeModelIssue(text);
  acc.qaIssue ||= looksLikeQAIssue(text);
  acc.contextIssue ||= looksLikeContextIssue(text);
  acc.permissionIssue ||= looksLikePermissionIssue(text);
  acc.terminalSuccess ||= isTerminalSuccess(event);
  if ((event.kind || '').includes('fail') || event.phase === 'error' || text.includes('failed') || text.includes('timeout') || text.includes('blocked')) {
    acc.failures += 1;
    if (acc.failureInsights.length < 4) {
      acc.failureInsights.push({
        severity: 'error',
        title: 'Failure event',
        detail: truncate(event.message || event.output || 'Failure detected in event log.', 220),
        phase: event.phase,
        task_id: event.task_id,
        agent: event.agent,
        time: event.time,
      });
    }
  }
}

function finishSummary(acc: SummaryAcc): RunEventSummary {
  const { retries, replans, count } = acc;
  const insights: NonNullable<RunEventSummary['insights']> = [...acc.failureInsights];
  const actions: NonNullable<RunEventSummary['actions']> = [];
  if (replans > 0) insights.push({ severity: 'info', title: 'Plan was revised', detail: `${replans} replan signal${replans === 1 ? '' : 's'} detected.` });
  if (retries >= 3) insights.push({ severity: 'warning', title: 'High retry pressure', detail: `${retries} retry signals detected; consider narrowing scope or using a larger local model.` });
  if (count > 0 && !acc.terminalSuccess) insights.push({ severity: 'warning', title: 'No successful terminal event', detail: 'The visible event window has no clear run_done marker.' });
  if (acc.providerIssue) actions.push({ title: 'Check the model endpoint', detail: 'The timeline looks like a provider or local runtime connectivity failure.', command: 'slmcode doctor' });
  if (acc.modelIssue) actions.push({ title: 'Verify the configured model', detail: 'The selected model may not be served by the current endpoint.', command: 'slmcode stack list' });
  if (acc.contextIssue || retries >= 3) actions.push({ title: 'Shrink the next attempt', detail: 'Use Request Replan or split the request into fewer files/tasks for the local model.' });
  if (acc.qaIssue) actions.push({ title: 'Run the project QA gate', detail: 'A test/build/lint gate appears to be the blocker.', command: 'slmcode status' });
  if (acc.permissionIssue) actions.push({ title: 'Review command permissions', detail: 'A shell or filesystem guardrail may have stopped execution.', command: 'slmcode config show' });
  if (count > 0 && !acc.terminalSuccess && actions.length === 0) actions.push({ title: 'Inspect the final phase', detail: 'The run did not record a clean terminal event; open the last error/output row before resuming.' });

  const final = acc.final;
  return {
    total_events: count,
    started_at: acc.first ? new Date(acc.first).toISOString() : undefined,
    last_at: acc.last ? new Date(acc.last).toISOString() : undefined,
    duration_ms: acc.first && acc.last ? acc.last - acc.first : undefined,
    final_phase: final?.phase,
    final_kind: final?.kind,
    last_message: final?.message,
    phases: rankCounts(acc.phases, 16),
    agents: rankCounts(acc.agents, 12),
    models: rankCounts(acc.models, 8),
    tasks: acc.tasks.size,
    retries,
    replans,
    failures: acc.failures,
    warnings: acc.warnings,
    errors: acc.errors,
    tool_calls: acc.toolCalls,
    shell_calls: acc.shellCalls,
    tokens: acc.tokens,
    cost_usd: acc.costUSD,
    insights: insights.slice(0, 8),
    actions: actions.slice(0, 5),
  };
}

/**
 * The log only ever grows by appending (the stream hook trims the head when
 * it passes its cap, and clears it on run_start), so the summary can fold
 * just the tail since last time: same first event and no fewer events means
 * the prefix is what we already counted. Anything else starts over.
 */
function useIncrementalSummary(events: RunEvent[]): RunEventSummary {
  const memo = useRef<{ events: RunEvent[]; acc: SummaryAcc; out: RunEventSummary } | null>(null);
  return useMemo(() => {
    const prev = memo.current;
    if (prev && prev.events === events) return prev.out;
    let acc: SummaryAcc;
    let from = 0;
    const sameHead = prev && prev.events.length > 0 && events.length >= prev.events.length && events[0] === prev.events[0] && events[prev.events.length - 1] === prev.events[prev.events.length - 1];
    if (prev && sameHead) {
      acc = prev.acc;
      from = prev.events.length;
    } else {
      acc = newSummaryAcc();
    }
    for (let i = from; i < events.length; i++) foldSummary(acc, events[i]);
    const out = finishSummary(acc);
    memo.current = { events, acc, out };
    return out;
  }, [events]);
}

function rankCounts(counts: Map<string, number>, limit: number) {
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, limit);
}

function isTerminalSuccess(event: RunEvent) {
  return event.kind === 'run_done' || event.kind === 'run_end' || (event.phase === 'done' && !String(event.message || '').toLowerCase().includes('stop'));
}

function looksLikeProviderIssue(text: string) {
  return text.includes('connection refused') || text.includes('connect:') || text.includes('no such host') || text.includes('econnrefused') || text.includes('server closed') || (text.includes('provider') && (text.includes('unreachable') || text.includes('failed')));
}

function looksLikeModelIssue(text: string) {
  return text.includes('model not found') || text.includes('unknown model') || text.includes('no model') || (text.includes('404') && text.includes('model'));
}

function looksLikeQAIssue(text: string) {
  return text.includes('qa_gate') || text.includes('test failed') || text.includes('lint failed') || text.includes('build failed') || text.includes('go test') || text.includes('npm test') || text.includes('pytest');
}

function looksLikeContextIssue(text: string) {
  return text.includes('context length') || text.includes('context window') || text.includes('maximum context') || text.includes('token limit') || text.includes('too many tokens') || text.includes('truncated');
}

function looksLikePermissionIssue(text: string) {
  return text.includes('permission denied') || text.includes('shell denied') || text.includes('not allowed') || text.includes('blocked by');
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch {
    return '--:--:--';
  }
}

function formatDuration(ms?: number): string {
  if (!ms || ms < 0) return '0s';
  if (ms < 1000) return `${ms}ms`;
  const total = Math.round(ms / 1000);
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes <= 0) return `${seconds}s`;
  return `${minutes}m ${seconds}s`;
}

function formatCost(cost?: number): string {
  if (!cost || cost <= 0) return '$0';
  if (cost < 0.01) return `$${cost.toFixed(4)}`;
  return `$${cost.toFixed(2)}`;
}

function truncate(value: string, limit: number): string {
  const s = value.trim();
  return s.length <= limit ? s : `${s.slice(0, limit)}...`;
}

// Memoised on props. The Live page re-renders on every stream flush — including
// token-only frames, where `events` keeps its identity and nothing here can
// have changed. Without this, each one re-ran summarizeEvents, the filters and
// compactAdjacentEvents over the whole log.
export default memo(EventLog);
