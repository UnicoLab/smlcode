import { memo, useMemo, useState } from 'react';
import type { RunEvent, RunEventSummary } from '@/types';
import clsx from 'clsx';

interface EventLogProps {
  events: RunEvent[];
  summary?: RunEventSummary | null;
}

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
  chips: { label: string; tone?: 'phase' | 'agent' | 'task' | 'file' | 'kind' }[];
};

function EventLog({ events, summary }: EventLogProps) {
  const [filter, setFilter] = useState<Filter>('all');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const insightSummary = useMemo(() => summary || summarizeEvents(events), [events, summary]);

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

      <div className="space-y-0.5 font-mono text-xs">
        {displayEvents.map((item, i) => {
          const event = item.event;
          const view = item.view;
          const rowKey = `${item.signature}-${event.time}-${i}`;
          const lvl = eventLevel(event);
          const style = LEVEL_STYLES[lvl] || LEVEL_STYLES.info;
          const isExpanded = expanded.has(rowKey);
          return (
            <div
              key={rowKey}
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
                    {view.chips.map((chip, idx) => (
                      <span
                        key={`${chip.label}-${idx}`}
                        className={clsx(
                          'rounded px-1.5 py-0.5 font-sans text-[9px] font-semibold',
                          chip.tone === 'agent' && 'bg-brand-50 text-brand-600 dark:bg-brand-950/40 dark:text-brand-300',
                          chip.tone === 'task' && 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400',
                          chip.tone === 'file' && 'bg-violet-50 text-violet-600 dark:bg-violet-950/40 dark:text-violet-300',
                          chip.tone === 'kind' && 'bg-sky-50 text-sky-600 dark:bg-sky-950/40 dark:text-sky-300',
                          (!chip.tone || chip.tone === 'phase') && 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400',
                        )}
                      >
                        {chip.label}
                      </span>
                    ))}
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
      </div>
    </div>
  );
}

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

function eventLevel(event: RunEvent) {
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

function eventSignature(event: RunEvent) {
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

function describeEvent(event: RunEvent): EventView {
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
  if (event.agent) chips.push({ label: `@${event.agent}`, tone: 'agent' });
  if (event.task_id) chips.push({ label: `#${event.task_id}`, tone: 'task' });
  if (file) chips.push({ label: file, tone: 'file' });
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

function summarizeEvents(events: RunEvent[]): RunEventSummary {
  const phases = new Map<string, number>();
  const agents = new Map<string, number>();
  const models = new Map<string, number>();
  const tasks = new Set<string>();
  let retries = 0;
  let replans = 0;
  let failures = 0;
  let warnings = 0;
  let errors = 0;
  let toolCalls = 0;
  let shellCalls = 0;
  let tokens = 0;
  let costUSD = 0;
  let first = 0;
  let last = 0;
  const insights: RunEventSummary['insights'] = [];
  const actions: NonNullable<RunEventSummary['actions']> = [];
  let providerIssue = false;
  let modelIssue = false;
  let qaIssue = false;
  let contextIssue = false;
  let permissionIssue = false;

  for (const event of events) {
    if (event.phase) phases.set(event.phase, (phases.get(event.phase) || 0) + 1);
    if (event.agent) agents.set(event.agent, (agents.get(event.agent) || 0) + 1);
    if (event.model) models.set(event.model, (models.get(event.model) || 0) + 1);
    if (event.task_id) tasks.add(event.task_id);
    if (typeof event.tokens === 'number' && event.tokens > 0) tokens += event.tokens;
    if (typeof event.cost_usd === 'number' && event.cost_usd > 0) costUSD += event.cost_usd;
    const t = Date.parse(event.time || '');
    if (!Number.isNaN(t)) {
      if (!first) first = t;
      last = t;
    }
    const text = `${event.phase || ''} ${event.kind || ''} ${event.message || ''} ${event.output || ''}`.toLowerCase();
    if (text.includes('retry') || text.includes('corrective')) retries += 1;
    if (text.includes('replan') || text.includes('plan was revised')) replans += 1;
    if (text.includes('warn') || text.includes('degraded')) warnings += 1;
    if (text.includes('error') || text.includes('panic') || text.includes('exception')) errors += 1;
    if ((event.kind || '').includes('tool')) toolCalls += 1;
    if ((event.kind || '').includes('shell') || text.includes('shell')) shellCalls += 1;
    providerIssue ||= looksLikeProviderIssue(text);
    modelIssue ||= looksLikeModelIssue(text);
    qaIssue ||= looksLikeQAIssue(text);
    contextIssue ||= looksLikeContextIssue(text);
    permissionIssue ||= looksLikePermissionIssue(text);
    if ((event.kind || '').includes('fail') || event.phase === 'error' || text.includes('failed') || text.includes('timeout') || text.includes('blocked')) {
      failures += 1;
      if (insights.length < 4) {
        insights.push({
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

  if (replans > 0) insights.push({ severity: 'info', title: 'Plan was revised', detail: `${replans} replan signal${replans === 1 ? '' : 's'} detected.` });
  if (retries >= 3) insights.push({ severity: 'warning', title: 'High retry pressure', detail: `${retries} retry signals detected; consider narrowing scope or using a larger local model.` });
  if (events.length > 0 && !hasTerminalSuccess(events)) insights.push({ severity: 'warning', title: 'No successful terminal event', detail: 'The visible event window has no clear run_done marker.' });
  if (providerIssue) actions.push({ title: 'Check the model endpoint', detail: 'The timeline looks like a provider or local runtime connectivity failure.', command: 'slmcode doctor' });
  if (modelIssue) actions.push({ title: 'Verify the configured model', detail: 'The selected model may not be served by the current endpoint.', command: 'slmcode stack list' });
  if (contextIssue || retries >= 3) actions.push({ title: 'Shrink the next attempt', detail: 'Use Request Replan or split the request into fewer files/tasks for the local model.' });
  if (qaIssue) actions.push({ title: 'Run the project QA gate', detail: 'A test/build/lint gate appears to be the blocker.', command: 'slmcode status' });
  if (permissionIssue) actions.push({ title: 'Review command permissions', detail: 'A shell or filesystem guardrail may have stopped execution.', command: 'slmcode config show' });
  if (events.length > 0 && !hasTerminalSuccess(events) && actions.length === 0) actions.push({ title: 'Inspect the final phase', detail: 'The run did not record a clean terminal event; open the last error/output row before resuming.' });

  const final = events[events.length - 1];
  return {
    total_events: events.length,
    started_at: first ? new Date(first).toISOString() : undefined,
    last_at: last ? new Date(last).toISOString() : undefined,
    duration_ms: first && last ? last - first : undefined,
    final_phase: final?.phase,
    final_kind: final?.kind,
    last_message: final?.message,
    phases: rankCounts(phases, 16),
    agents: rankCounts(agents, 12),
    models: rankCounts(models, 8),
    tasks: tasks.size,
    retries,
    replans,
    failures,
    warnings,
    errors,
    tool_calls: toolCalls,
    shell_calls: shellCalls,
    tokens,
    cost_usd: costUSD,
    insights: insights.slice(0, 8),
    actions: actions.slice(0, 5),
  };
}

function rankCounts(counts: Map<string, number>, limit: number) {
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, limit);
}

function hasTerminalSuccess(events: RunEvent[]) {
  return events.some((event) => event.kind === 'run_done' || event.kind === 'run_end' || (event.phase === 'done' && !String(event.message || '').toLowerCase().includes('stop')));
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
