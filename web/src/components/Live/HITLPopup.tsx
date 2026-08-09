import { useState, useEffect, useCallback, useRef } from 'react';
import {
  Clock,
  AlertTriangle,
  HelpCircle,
  Check,
  X,
  ChevronRight,
  Terminal,
  ClipboardList,
  RefreshCw,
  AlertOctagon,
} from 'lucide-react';
import clsx from 'clsx';
import {
  getClarifyPending,
  getPlanPending,
  getContinuePending,
  getEscalatePending,
  getShellPending,
  answerClarify,
  clarifyUseRecommended,
  approvePlan,
  answerContinue,
  answerEscalate,
  approveShell,
} from '@/api/client';
import type {
  ClarifyAsk,
  PlanAsk,
  ContinueAsk,
  EscalateAsk,
  ShellAsk,
} from '@/types';

// ── HITL type union ──
type HITLType = 'clarify' | 'plan' | 'continue' | 'escalate' | 'shell';

interface PendingState {
  type: HITLType;
  data: ClarifyAsk | PlanAsk | ContinueAsk | EscalateAsk | ShellAsk;
}

// ── Timer durations in seconds ──
const TIMEOUTS: Record<HITLType, number> = {
  clarify: 120,
  plan: 120,
  continue: 120,
  escalate: 30,
  shell: 120,
};

// ── Type visual config ──
const TYPE_LABELS: Record<HITLType, string> = {
  clarify: 'Clarify',
  plan: 'Plan Approval',
  continue: 'Continue?',
  escalate: 'Escalation',
  shell: 'Shell Command',
};

const TYPE_ICONS: Record<HITLType, React.ReactNode> = {
  clarify: <HelpCircle className="w-5 h-5" />,
  plan: <ClipboardList className="w-5 h-5" />,
  continue: <RefreshCw className="w-5 h-5" />,
  escalate: <AlertOctagon className="w-5 h-5" />,
  shell: <Terminal className="w-5 h-5" />,
};

const TYPE_COLORS: Record<HITLType, string> = {
  clarify: 'purple',
  plan: 'fuchsia',
  continue: 'amber',
  escalate: 'red',
  shell: 'cyan',
};

const COLOR_RING: Record<string, string> = {
  purple: 'ring-purple-500/30',
  fuchsia: 'ring-fuchsia-500/30',
  amber: 'ring-amber-500/30',
  red: 'ring-red-500/30',
  cyan: 'ring-cyan-500/30',
};

const COLOR_BORDER: Record<string, string> = {
  purple: 'border-purple-500/40',
  fuchsia: 'border-fuchsia-500/40',
  amber: 'border-amber-500/40',
  red: 'border-red-500/40',
  cyan: 'border-cyan-500/40',
};

const COLOR_BG_ICON: Record<string, string> = {
  purple: 'bg-purple-100 dark:bg-purple-900/40 text-purple-600 dark:text-purple-400',
  fuchsia: 'bg-fuchsia-100 dark:bg-fuchsia-900/40 text-fuchsia-600 dark:text-fuchsia-400',
  amber: 'bg-amber-100 dark:bg-amber-900/40 text-amber-600 dark:text-amber-400',
  red: 'bg-red-100 dark:bg-red-900/40 text-red-600 dark:text-red-400',
  cyan: 'bg-cyan-100 dark:bg-cyan-900/40 text-cyan-600 dark:text-cyan-400',
};

const COLOR_PROGRESS_BG: Record<string, string> = {
  purple: 'bg-purple-500',
  fuchsia: 'bg-fuchsia-500',
  amber: 'bg-amber-500',
  red: 'bg-red-500',
  cyan: 'bg-cyan-500',
};

const COLOR_PROGRESS_TRACK: Record<string, string> = {
  purple: 'bg-purple-200 dark:bg-purple-900/50',
  fuchsia: 'bg-fuchsia-200 dark:bg-fuchsia-900/50',
  amber: 'bg-amber-200 dark:bg-amber-900/50',
  red: 'bg-red-200 dark:bg-red-900/50',
  cyan: 'bg-cyan-200 dark:bg-cyan-900/50',
};

const COLOR_BTN_PRIMARY: Record<string, string> = {
  purple: 'bg-purple-600 hover:bg-purple-700 text-white',
  fuchsia: 'bg-fuchsia-600 hover:bg-fuchsia-700 text-white',
  amber: 'bg-amber-600 hover:bg-amber-700 text-white',
  red: 'bg-red-600 hover:bg-red-700 text-white',
  cyan: 'bg-cyan-600 hover:bg-cyan-700 text-white',
};

const COLOR_BTN_GHOST: Record<string, string> = {
  purple: 'border-purple-300 dark:border-purple-700 text-purple-700 dark:text-purple-300 hover:bg-purple-50 dark:hover:bg-purple-900/30',
  fuchsia: 'border-fuchsia-300 dark:border-fuchsia-700 text-fuchsia-700 dark:text-fuchsia-300 hover:bg-fuchsia-50 dark:hover:bg-fuchsia-900/30',
  amber: 'border-amber-300 dark:border-amber-700 text-amber-700 dark:text-amber-300 hover:bg-amber-50 dark:hover:bg-amber-900/30',
  red: 'border-red-300 dark:border-red-700 text-red-700 dark:text-red-300 hover:bg-red-50 dark:hover:bg-red-900/30',
  cyan: 'border-cyan-300 dark:border-cyan-700 text-cyan-700 dark:text-cyan-300 hover:bg-cyan-50 dark:hover:bg-cyan-900/30',
};

// ── Props ──
interface HITLPopupProps {
  running: boolean;
}

export default function HITLPopup({ running }: HITLPopupProps) {
  const [pending, setPending] = useState<PendingState | null>(null);
  const [countdown, setCountdown] = useState(0);
  const [answering, setAnswering] = useState(false);

  // ── Clarify-specific state ──
  const [clarifySelections, setClarifySelections] = useState<Record<string, string[]>>({});

  // Refs to prevent double-answering
  const answeredRef = useRef(false);
  const pendingRef = useRef<PendingState | null>(null);
  pendingRef.current = pending;

  // ── Poll all 5 HITL endpoints ──
  useEffect(() => {
    if (!running) {
      setPending(null);
      setCountdown(0);
      answeredRef.current = false;
      return;
    }

    let active = true;

    const poll = async () => {
      if (!active || answeredRef.current) return;

      try {
        const results = await Promise.allSettled([
          getClarifyPending(),
          getPlanPending(),
          getContinuePending(),
          getEscalatePending(),
          getShellPending(),
        ]);

        const types: HITLType[] = ['clarify', 'plan', 'continue', 'escalate', 'shell'];

        for (let i = 0; i < results.length; i++) {
          const r = results[i];
          if (r.status === 'fulfilled' && r.value.pending && r.value.ask) {
            if (!active || answeredRef.current) return;

            const type = types[i];
            setPending({ type, data: r.value.ask });
            setCountdown(TIMEOUTS[type]);
            answeredRef.current = false;

            // Init clarify selections from recommended
            if (type === 'clarify') {
              const ask = r.value.ask as ClarifyAsk;
              const sel: Record<string, string[]> = {};
              for (const q of ask.questions) {
                sel[q.id] = q.recommended ? [q.recommended] : [];
              }
              setClarifySelections(sel);
            }

            return; // Only show first pending
          }
        }
      } catch {
        // Poll silently fails, retry next cycle
      }
    };

    // Poll immediately, then every 2s
    poll();
    const interval = setInterval(poll, 2000);
    return () => {
      active = false;
      clearInterval(interval);
    };
  }, [running]);

  // ── Countdown timer ──
  useEffect(() => {
    if (!pending || countdown <= 0) return;

    const interval = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          clearInterval(interval);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);

    return () => clearInterval(interval);
  }, [pending, countdown]);

  // ── Auto-answer on timeout ──
  useEffect(() => {
    if (!pending || countdown > 0 || answeredRef.current) return;

    answeredRef.current = true;
    handleDefaultAnswer(pending);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [countdown]);

  // ── Default / recommended answer ──
  const handleDefaultAnswer = useCallback(async (p: PendingState) => {
    setAnswering(true);
    try {
      switch (p.type) {
        case 'clarify':
          await clarifyUseRecommended();
          break;
        case 'plan':
          await approvePlan('approve');
          break;
        case 'continue':
          await answerContinue('continue');
          break;
        case 'escalate':
          await answerEscalate('retry');
          break;
        case 'shell':
          await approveShell('approve');
          break;
      }
    } catch {
      // Best-effort
    }
    setPending(null);
    setCountdown(0);
    setAnswering(false);
    answeredRef.current = false;
  }, []);

  // ── User-driven answer ──
  const handleAnswer = useCallback(async (action: string) => {
    if (!pending || answeredRef.current) return;
    answeredRef.current = true;
    setAnswering(true);

    try {
      switch (pending.type) {
        case 'clarify': {
          const answers = Object.entries(clarifySelections).map(([qid, sel]) => ({
            question_id: qid,
            selected: sel,
          }));
          // If user clicked "Use Recommended", call dedicated endpoint
          if (action === 'recommended') {
            await clarifyUseRecommended();
          } else {
            await answerClarify(answers);
          }
          break;
        }
        case 'plan':
          await approvePlan(action as 'approve' | 'replan');
          break;
        case 'continue':
          await answerContinue(action as 'continue' | 'stop' | 'flag_only');
          break;
        case 'escalate':
          await answerEscalate(action as 'retry' | 're_scope' | 'mark_done' | 'abort');
          break;
        case 'shell':
          await approveShell(action as 'approve' | 'deny');
          break;
      }
    } catch {
      // Best-effort
    }

    setPending(null);
    setCountdown(0);
    setAnswering(false);
    answeredRef.current = false;
  }, [pending, clarifySelections]);

  // ── Toggle clarify checkbox ──
  const toggleClarifyOption = useCallback((questionId: string, label: string) => {
    setClarifySelections((prev) => {
      const current = prev[questionId] || [];
      if (current.includes(label)) {
        return { ...prev, [questionId]: current.filter((l) => l !== label) };
      }
      return { ...prev, [questionId]: [...current, label] };
    });
  }, []);

  // ── Nothing to show ──
  if (!pending || !running) return null;

  const color = TYPE_COLORS[pending.type];
  const timeoutSec = TIMEOUTS[pending.type];
  const progressPct = timeoutSec > 0 ? (countdown / timeoutSec) * 100 : 0;
  const urgency = countdown <= 10;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" />

      {/* Modal */}
      <div
        className={clsx(
          'relative w-full max-w-lg mx-4 rounded-2xl shadow-2xl ring-1',
          'bg-white dark:bg-gray-900',
          COLOR_RING[color],
          COLOR_BORDER[color],
        )}
      >
        {/* Header */}
        <div className="flex items-center gap-3 px-6 pt-5 pb-3">
          <div
            className={clsx(
              'flex items-center justify-center w-10 h-10 rounded-xl',
              COLOR_BG_ICON[color],
            )}
          >
            {TYPE_ICONS[pending.type]}
          </div>
          <div className="flex-1 min-w-0">
            <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">
              {TYPE_LABELS[pending.type]}
            </h2>
            <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
              Agent needs your input
            </p>
          </div>

          {/* Timer badge */}
          <div
            className={clsx(
              'flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-mono font-semibold',
              urgency
                ? 'bg-red-100 dark:bg-red-900/40 text-red-600 dark:text-red-400 animate-pulse'
                : 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400',
            )}
          >
            <Clock className={clsx('w-3.5 h-3.5', urgency && 'animate-pulse')} />
            {formatSeconds(countdown)}
          </div>
        </div>

        {/* Countdown progress bar */}
        <div className={clsx('h-1 mx-6 rounded-full overflow-hidden', COLOR_PROGRESS_TRACK[color])}>
          <div
            className={clsx(
              'h-full rounded-full transition-all duration-1000 ease-linear',
              COLOR_PROGRESS_BG[color],
              urgency && 'animate-pulse',
            )}
            style={{ width: `${progressPct}%` }}
          />
        </div>

        {/* Body — type-specific */}
        <div className="px-6 py-4">
          {pending.type === 'clarify' && (
            <ClarifyBody
              data={pending.data as ClarifyAsk}
              selections={clarifySelections}
              onToggle={toggleClarifyOption}
            />
          )}
          {pending.type === 'plan' && <PlanBody data={pending.data as PlanAsk} />}
          {pending.type === 'continue' && <ContinueBody data={pending.data as ContinueAsk} />}
          {pending.type === 'escalate' && <EscalateBody data={pending.data as EscalateAsk} />}
          {pending.type === 'shell' && <ShellBody data={pending.data as ShellAsk} />}
        </div>

        {/* Actions */}
        <div className="px-6 pb-5 space-y-2">
          {pending.type === 'clarify' && (
            <ClarifyActions
              color={color}
              answering={answering}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'plan' && (
            <PlanActions
              color={color}
              answering={answering}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'continue' && (
            <ContinueActions
              color={color}
              answering={answering}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'escalate' && (
            <EscalateActions
              color={color}
              answering={answering}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'shell' && (
            <ShellActions
              color={color}
              answering={answering}
              onAnswer={handleAnswer}
            />
          )}
        </div>
      </div>
    </div>
  );
}

// ── Body renderers ──

function ClarifyBody({
  data,
  selections,
  onToggle,
}: {
  data: ClarifyAsk;
  selections: Record<string, string[]>;
  onToggle: (questionId: string, label: string) => void;
}) {
  return (
    <div className="space-y-4">
      {data.questions.map((q: ClarifyAsk['questions'][number]) => (
        <div key={q.id}>
          <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-1">
            {q.header}
          </h3>
          <p className="text-xs text-gray-500 dark:text-gray-400 mb-2">
            {q.question}
          </p>
          <div className="space-y-1.5">
            {q.options.map((opt: typeof q.options[number]) => {
              const checked = (selections[q.id] || []).includes(opt.label);
              return (
                <label
                  key={opt.label}
                  className={clsx(
                    'flex items-start gap-2.5 px-3 py-2 rounded-lg cursor-pointer transition-colors border',
                    checked
                      ? 'border-purple-400 dark:border-purple-600 bg-purple-50 dark:bg-purple-900/20'
                      : 'border-gray-200 dark:border-gray-700 hover:border-purple-300 dark:hover:border-purple-700',
                  )}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => onToggle(q.id, opt.label)}
                    className="mt-0.5 w-4 h-4 rounded border-gray-300 dark:border-gray-600
                               text-purple-600 focus:ring-purple-500 dark:bg-gray-800"
                  />
                  <div className="flex-1 min-w-0">
                    <span
                      className={clsx(
                        'text-sm font-medium',
                        checked
                          ? 'text-purple-800 dark:text-purple-200'
                          : 'text-gray-700 dark:text-gray-300',
                      )}
                    >
                      {opt.label}
                      {opt.recommended && (
                        <span className="ml-1.5 text-[10px] font-semibold uppercase tracking-wide text-purple-500 dark:text-purple-400">
                          recommended
                        </span>
                      )}
                    </span>
                    {opt.description && (
                      <p className="text-xs text-gray-400 dark:text-gray-500 mt-0.5">
                        {opt.description}
                      </p>
                    )}
                  </div>
                </label>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}

function PlanBody({ data }: { data: PlanAsk }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700 dark:text-gray-300">{data.summary}</p>
      <div className="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
        <ClipboardList className="w-3.5 h-3.5" />
        <span>
          {data.task_count} task{data.task_count !== 1 ? 's' : ''} in plan
        </span>
      </div>
      {data.tasks.length > 0 && (
        <div className="max-h-32 overflow-y-auto space-y-1">
          {data.tasks.map((t: string, i: number) => (
            <div
              key={i}
              className="flex items-start gap-2 text-xs text-gray-600 dark:text-gray-400"
            >
              <ChevronRight className="w-3 h-3 mt-0.5 shrink-0 text-gray-400" />
              <span className="truncate">{t}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ContinueBody({ data }: { data: ContinueAsk }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700 dark:text-gray-300">{data.summary}</p>
      {data.reason && (
        <div className="flex items-start gap-2 p-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800">
          <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0 mt-0.5" />
          <p className="text-xs text-amber-700 dark:text-amber-300">{data.reason}</p>
        </div>
      )}
      {data.escalated.length > 0 && (
        <div>
          <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">
            Escalated tasks:
          </p>
          {data.escalated.map((t: string, i: number) => (
            <div key={i} className="text-xs text-red-600 dark:text-red-400 ml-2">
              • {t}
            </div>
          ))}
        </div>
      )}
      {data.gaps.length > 0 && (
        <div>
          <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">
            Gaps found:
          </p>
          {data.gaps.map((g: string, i: number) => (
            <div key={i} className="text-xs text-amber-600 dark:text-amber-400 ml-2">
              • {g}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function EscalateBody({ data }: { data: EscalateAsk }) {
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <span className="px-2 py-0.5 text-[10px] font-semibold uppercase rounded bg-red-100 dark:bg-red-900/40 text-red-600 dark:text-red-400">
          {data.kind || 'escalation'}
        </span>
        <span className="text-xs text-gray-400 dark:text-gray-500 font-mono">
          #{data.task_id}
        </span>
      </div>
      <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200">
        {data.title}
      </h3>
      <p className="text-xs text-gray-600 dark:text-gray-400">{data.detail}</p>
      {data.summary && (
        <div className="p-2.5 rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
          <p className="text-xs text-red-700 dark:text-red-300">{data.summary}</p>
        </div>
      )}
      <div className="flex flex-wrap gap-1.5">
        <span className="text-[10px] text-gray-400 dark:text-gray-500">
          Role: {data.role}
        </span>
        {data.files.length > 0 && (
          <span className="text-[10px] text-gray-400 dark:text-gray-500">
            Files: {data.files.join(', ')}
          </span>
        )}
      </div>
    </div>
  );
}

function ShellBody({ data }: { data: ShellAsk }) {
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 mb-1">
        <Terminal className="w-4 h-4 text-cyan-500" />
        <span className="text-xs text-gray-400 dark:text-gray-500 font-mono">
          {data.task_id ? `Task #${data.task_id}` : 'Shell command'}
        </span>
      </div>
      <pre className="p-3 rounded-lg bg-gray-900 dark:bg-gray-950 text-green-400 text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all">
        {data.command}
      </pre>
    </div>
  );
}

// ── Action renderers ──

function ClarifyActions({
  color,
  answering,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        disabled={answering}
        onClick={() => onAnswer('submit')}
        className={clsx(
          'flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all',
          COLOR_BTN_PRIMARY[color],
          'disabled:opacity-50',
        )}
      >
        <Check className="w-4 h-4" />
        {answering ? 'Submitting…' : 'Submit Answers'}
      </button>
      <button
        disabled={answering}
        onClick={() => onAnswer('recommended')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          COLOR_BTN_GHOST[color],
          'disabled:opacity-50',
        )}
      >
        Use Recommended
      </button>
    </div>
  );
}

function PlanActions({
  color,
  answering,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        disabled={answering}
        onClick={() => onAnswer('approve')}
        className={clsx(
          'flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all',
          COLOR_BTN_PRIMARY[color],
          'disabled:opacity-50',
        )}
      >
        <Check className="w-4 h-4" />
        {answering ? 'Approving…' : 'Approve Plan'}
      </button>
      <button
        disabled={answering}
        onClick={() => onAnswer('replan')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          COLOR_BTN_GHOST[color],
          'disabled:opacity-50',
        )}
      >
        <RefreshCw className="w-4 h-4" />
      </button>
    </div>
  );
}

function ContinueActions({
  color,
  answering,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        disabled={answering}
        onClick={() => onAnswer('continue')}
        className={clsx(
          'flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all',
          COLOR_BTN_PRIMARY[color],
          'disabled:opacity-50',
        )}
      >
        <Check className="w-4 h-4" />
        {answering ? 'Continuing…' : 'Continue'}
      </button>
      <button
        disabled={answering}
        onClick={() => onAnswer('stop')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          COLOR_BTN_GHOST[color],
          'disabled:opacity-50',
        )}
      >
        <X className="w-4 h-4" />
      </button>
      <button
        disabled={answering}
        onClick={() => onAnswer('flag_only')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          COLOR_BTN_GHOST[color],
          'disabled:opacity-50',
        )}
      >
        Flag
      </button>
    </div>
  );
}

function EscalateActions({
  color,
  answering,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <button
          disabled={answering}
          onClick={() => onAnswer('retry')}
          className={clsx(
            'flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all',
            COLOR_BTN_PRIMARY[color],
            'disabled:opacity-50',
          )}
        >
          <RefreshCw className="w-4 h-4" />
          {answering ? 'Retrying…' : 'Retry'}
        </button>
        <button
          disabled={answering}
          onClick={() => onAnswer('re_scope')}
          className={clsx(
            'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
            COLOR_BTN_GHOST[color],
            'disabled:opacity-50',
          )}
        >
          Re-scope
        </button>
      </div>
      <div className="flex items-center gap-2">
        <button
          disabled={answering}
          onClick={() => onAnswer('mark_done')}
          className={clsx(
            'flex-1 px-4 py-2 rounded-xl text-xs font-medium border transition-all',
            COLOR_BTN_GHOST[color],
            'disabled:opacity-50',
          )}
        >
          Mark Done
        </button>
        <button
          disabled={answering}
          onClick={() => onAnswer('abort')}
          className={clsx(
            'flex-1 px-4 py-2 rounded-xl text-xs font-medium border transition-all',
            'border-red-300 dark:border-red-800 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30',
            'disabled:opacity-50',
          )}
        >
          Abort
        </button>
      </div>
    </div>
  );
}

function ShellActions({
  color,
  answering,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        disabled={answering}
        onClick={() => onAnswer('approve')}
        className={clsx(
          'flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold transition-all',
          COLOR_BTN_PRIMARY[color],
          'disabled:opacity-50',
        )}
      >
        <Check className="w-4 h-4" />
        {answering ? 'Approving…' : 'Approve'}
      </button>
      <button
        disabled={answering}
        onClick={() => onAnswer('deny')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          'border-red-300 dark:border-red-800 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30',
          'disabled:opacity-50',
        )}
      >
        <X className="w-4 h-4" />
      </button>
    </div>
  );
}

// ── Helpers ──

function formatSeconds(total: number): string {
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}
