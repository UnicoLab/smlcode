import { useMemo, useState } from 'react';
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
  run_start: '🚀',
  run_done: '✅',
  run_error: '❌',
  task_start: '▶️',
  task_done: '✔️',
  task_fail: '❌',
  review: '👁️',
  correct: '🔧',
  coord: '🎯',
  plan: '📋',
  explore: '🔍',
  context: '📝',
  clarify: '❓',
  split: '✂️',
  polish: '✨',
  test: '🧪',
  memory: '🧠',
  agent: '🤖',
  llm: '💭',
  tool: '🔨',
  shell: '💻',
  wave: '🌊',
  gate: '🚧',
  rewind: '⏪',
};

// Severity styling — problems/warnings/errors are visually distinct from routine info.
const LEVEL_STYLES: Record<string, { row: string; badge: string; label: string; icon: string }> = {
  error: { row: 'bg-red-50/70 dark:bg-red-900/15', badge: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300', label: 'ERROR', icon: '❌' },
  problem: { row: 'bg-orange-50/70 dark:bg-orange-900/15', badge: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300', label: 'PROBLEM', icon: '⚠️' },
  warning: { row: 'bg-amber-50/60 dark:bg-amber-900/10', badge: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300', label: 'WARN', icon: '⚠️' },
  success: { row: 'bg-green-50/60 dark:bg-green-900/10', badge: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300', label: 'OK', icon: '✅' },
  info: { row: '', badge: 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400', label: 'INFO', icon: '·' },
};

type Filter = 'all' | 'problems';

export default function EventLog({ events, summary }: EventLogProps) {
  const [filter, setFilter] = useState<Filter>('all');
  const insightSummary = useMemo(() => summary || summarizeEvents(events), [events, summary]);

  const counts = useMemo(() => {
    const c = { error: 0, problem: 0, warning: 0, success: 0 };
    for (const e of events) {
      const lvl = e.level || 'info';
      if (lvl in c) c[lvl as keyof typeof c] += 1;
    }
    return c;
  }, [events]);

  const visible = useMemo(() => {
    if (filter === 'all') return events;
    return events.filter((e) => {
      const lvl = e.level || 'info';
      return lvl === 'error' || lvl === 'problem' || lvl === 'warning';
    });
  }, [events, filter]);

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
          {counts.error > 0 && <span className="text-red-500">❌ {counts.error}</span>}
          {counts.problem > 0 && <span className="text-orange-500">⚠️ {counts.problem}</span>}
          {counts.warning > 0 && <span className="text-amber-500">⚠️ {counts.warning}</span>}
          {counts.success > 0 && <span className="text-green-500">✅ {counts.success}</span>}
        </span>
      </div>

      <div className="space-y-0.5 font-mono text-xs">
        {visible.map((event, i) => {
          const lvl = event.level || 'info';
          const style = LEVEL_STYLES[lvl] || LEVEL_STYLES.info;
          return (
            <div
              key={`${event.time}-${i}`}
              className={clsx(
                'flex items-start gap-3 px-3 py-2 rounded-lg transition-colors hover:bg-gray-50 dark:hover:bg-gray-800/50',
                style.row,
                event.phase === 'error' && 'bg-red-50/50 dark:bg-red-900/10',
              )}
            >
              {/* Severity icon (preferred over kind icon for non-info levels) */}
              <span className="shrink-0 mt-px text-sm" title={lvl}>
                {lvl !== 'info' ? style.icon : KIND_ICONS[event.kind] || '·'}
              </span>

              {/* Timestamp */}
              <span className="text-gray-400 dark:text-gray-600 shrink-0 w-20 tabular-nums">
                {formatTime(event.time)}
              </span>

              {/* Severity badge */}
              <span
                className={clsx(
                  'shrink-0 px-1.5 py-0.5 rounded text-[9px] font-bold leading-none',
                  style.badge,
                )}
              >
                {style.label}
              </span>

              {/* Phase badge */}
              <span
                className={clsx(
                  'shrink-0 w-20 text-right text-[10px] font-semibold uppercase tracking-wider',
                  PHASE_COLORS[event.phase] || 'text-gray-500',
                )}
              >
                {event.phase}
              </span>

              {/* Message */}
              <span className="flex-1 text-gray-700 dark:text-gray-300 break-words">
                {event.agent && (
                  <span className="text-brand-500 font-semibold">[{event.agent}] </span>
                )}
                {event.task_id && (
                  <span className="text-gray-400">#{event.task_id} </span>
                )}
                {event.scope && (
                  <span className="text-violet-500">({event.scope}) </span>
                )}
                {event.message}
              </span>

              {/* Output preview */}
              {event.output && (
                <span className="text-gray-400 truncate max-w-[200px] ml-2">
                  {event.output.slice(0, 80)}
                </span>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
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
