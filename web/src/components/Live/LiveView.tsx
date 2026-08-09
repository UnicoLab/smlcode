import { useState, useEffect, useRef, useCallback, useContext } from 'react';
import { Play, Square, RefreshCw } from 'lucide-react';
import { AppContext } from '@/App';
import { startRun, stopRun, getLatestRun, getAgents, createEventSource } from '@/api/client';
import type { RunEvent, LatestRunResponse, AgentSpec } from '@/types';
import EventLog from './EventLog';
import clsx from 'clsx';

export default function LiveView() {
  const ctx = useContext(AppContext);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<LatestRunResponse | null>(null);
  const [query, setQuery] = useState('');
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const eventSource = useRef<EventSource | null>(null);
  const logEnd = useRef<HTMLDivElement>(null);

  // Scroll to bottom on new events
  useEffect(() => {
    logEnd.current?.scrollIntoView({ behavior: 'smooth' });
  }, [events]);

  // Connect SSE
  const connectSSE = useCallback(() => {
    if (eventSource.current) {
      eventSource.current.close();
    }
    const es = new EventSource('/api/events');
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data);
        if (data.phase === 'done' || data.phase === 'error') {
          setRunning(false);
          getLatestRun().then(setResult).catch(() => {});
        }
        if (data.kind === 'run_start') {
          setEvents([]);
        }
        setEvents((prev) => [...prev.slice(-500), data]);
      } catch { /* ignore */ }
    };
    es.onerror = () => {
      es.close();
      // Reconnect after 2s
      setTimeout(() => {
        if (eventSource.current === es) connectSSE();
      }, 2000);
    };
    eventSource.current = es;
  }, []);

  useEffect(() => {
    connectSSE();
    getLatestRun().then(setResult).catch(() => {});
    return () => {
      eventSource.current?.close();
    };
  }, [connectSSE]);

  // Load agents for specialist picker
  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  const handleRun = async () => {
    const q = query.trim();
    if (!q || running) return;
    setEvents([]);
    setResult(null);
    setRunning(true);
    try {
      await startRun({
        query: q,
        mode: specialist ? 'specialist' : undefined,
        specialist: specialist || undefined,
        skills: ctx?.config?.pinned_skills,
      });
    } catch (e) {
      console.error('Run failed:', e);
      setRunning(false);
    }
  };

  const handleStop = async () => {
    try {
      await stopRun();
    } catch { /* ignore */ }
    setRunning(false);
  };

  return (
    <div className="h-full flex flex-col">
      {/* Run bar */}
      <div className="p-4 border-b border-gray-200 dark:border-gray-800 glass-alt">
        <div className="flex items-center gap-3 max-w-3xl">
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleRun()}
            placeholder="What would you like to build?"
            className="input flex-1 h-10"
            disabled={running}
          />
          {/* Specialist picker */}
          {agents.length > 0 && (
            <select
              value={specialist}
              onChange={(e) => setSpecialist(e.target.value)}
              className="input w-40 h-10"
              disabled={running}
            >
              <option value="">Any agent</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.title || a.id}
                  {a.effective_model ? ` · ${a.effective_model}` : ''}
                </option>
              ))}
            </select>
          )}
          {running ? (
            <button onClick={handleStop} className="btn-danger h-10 px-4 gap-2">
              <Square size={16} fill="currentColor" />
              Stop
            </button>
          ) : (
            <button
              onClick={handleRun}
              disabled={!query.trim()}
              className="btn-primary h-10 px-6 gap-2"
            >
              <Play size={16} fill="currentColor" />
              Run
            </button>
          )}
        </div>
      </div>

      {/* Active config indicator */}
      {(ctx?.config?.active_pack || ctx?.config?.active_stack || ctx?.config?.active_pipeline) && (
        <div className="px-4 py-1.5 glass-alt border-b border-gray-100 dark:border-gray-700 flex items-center gap-3">
          <span className="text-[10px] text-gray-400 font-medium">Active:</span>
          {ctx.config.active_pack && (
            <span className="badge-brand text-[10px]">📦 {ctx.config.active_pack}</span>
          )}
          {ctx.config.active_pipeline && (
            <span className="badge-neutral text-[10px]">{ctx.config.active_pipeline}</span>
          )}
          {ctx.config.active_stack && (
            <span className="badge-neutral text-[10px]">⚡ {ctx.config.active_stack}</span>
          )}
        </div>
      )}

      {/* Content area */}
      <div className="flex-1 flex overflow-hidden">
        {/* Event log */}
        <div className="flex-1 overflow-auto p-4">
          {events.length === 0 && !result ? (
            <div className="flex flex-col items-center justify-center h-full text-center space-y-4">
              <div className="w-16 h-16 rounded-2xl bg-brand-100 dark:bg-brand-900/30 flex items-center justify-center">
                <Play size={28} className="text-brand-500" />
              </div>
              <div>
                <h2 className="text-lg font-semibold text-gray-700 dark:text-gray-300">SLMCode Studio</h2>
                <p className="text-sm text-gray-400 mt-1 max-w-sm">
                  Enter a query above to start the agent pipeline.
                  Watch live progress via SSE streaming.
                </p>
              </div>
            </div>
          ) : (
            <EventLog events={events} />
          )}
          <div ref={logEnd} />
        </div>

        {/* Result sidebar */}
        {result && result.result && (
          <div className="w-80 border-l border-gray-200 dark:border-gray-800 p-4 overflow-auto shrink-0 animate-slide-left">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-bold">Result</h3>
              <span
                className={clsx(
                  'badge text-[10px]',
                  result.result.success ? 'badge-success' : 'badge-error',
                )}
              >
                {result.result.success ? 'Success' : 'Failed'}
              </span>
            </div>

            <div className="space-y-3">
              {result.result.summary && (
                <div>
                  <div className="label">Summary</div>
                  <p className="text-sm text-gray-600 dark:text-gray-400">{result.result.summary}</p>
                </div>
              )}

              <div className="grid grid-cols-2 gap-2">
                <div className="card p-2">
                  <div className="text-[10px] text-gray-400">Failed tasks</div>
                  <div className={clsx('text-lg font-bold', result.result.failed_tasks > 0 && 'text-red-500')}>
                    {result.result.failed_tasks}
                  </div>
                </div>
                <div className="card p-2">
                  <div className="text-[10px] text-gray-400">Duration</div>
                  <div className="text-sm font-mono font-medium">
                    {result.result.duration > 1e9
                      ? (result.result.duration / 1e9).toFixed(1) + 's'
                      : (result.result.duration / 1e6).toFixed(0) + 'ms'}
                  </div>
                </div>
              </div>

              {/* Events count */}
              <div className="card p-2">
                <div className="text-[10px] text-gray-400">Events</div>
                <div className="text-lg font-bold">{result.events?.length || 0}</div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
