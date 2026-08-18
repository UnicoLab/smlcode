import { useState, useEffect, useCallback, useRef, useId } from 'react';
import type { RefObject } from 'react';
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
  PlanApprovalTask,
  ContinueAsk,
  DynamicComposition,
  EscalateAsk,
  ShellAsk,
} from '@/types';

// ── HITL type union ──
type HITLType = 'clarify' | 'plan' | 'continue' | 'escalate' | 'shell';

interface PendingState {
  type: HITLType;
  data: ClarifyAsk | PlanAsk | ContinueAsk | EscalateAsk | ShellAsk;
}

interface AskMetadata {
  id?: string;
  kind?: string;
  query?: string;
  created_at?: string;
  timeout_sec?: number;
  on_timeout?: string;
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
  const [pendingQueue, setPendingQueue] = useState<PendingState[]>([]);
  const [countdown, setCountdown] = useState(0);
  const [answering, setAnswering] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [syncNotice, setSyncNotice] = useState<string | null>(null);
  const [hitlNotes, setHitlNotes] = useState('');

  // ── Clarify-specific state ──
  const [clarifySelections, setClarifySelections] = useState<Record<string, string[]>>({});
  const [clarifyFreeform, setClarifyFreeform] = useState<Record<string, string>>({});

  // Refs to prevent double-answering
  const answeredRef = useRef(false);
  const answeredKeysRef = useRef<Set<string>>(new Set());
  const pendingRef = useRef<PendingState | null>(null);
  const firstActionRef = useRef<HTMLButtonElement | null>(null);
  const titleId = useId();
  pendingRef.current = pending;

  // ── Poll all 5 HITL endpoints ──
  useEffect(() => {
    if (!running) {
      setPending(null);
      setPendingQueue([]);
      setCountdown(0);
      answeredRef.current = false;
      answeredKeysRef.current.clear();
      setSubmitError(null);
      setSyncNotice(null);
      setHitlNotes('');
      setClarifyFreeform({});
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

        const syncErrors: string[] = [];
        const expired: HITLType[] = [];
        let answered = false;
        const pendingItems: PendingState[] = [];
        for (let i = 0; i < results.length; i++) {
          const r = results[i];
          const type = types[i];
          if (r.status === 'fulfilled' && r.value.expired) {
            expired.push(type);
          }
          if (r.status === 'fulfilled' && r.value.answered) {
            answered = true;
          }
          if (r.status === 'rejected') {
            syncErrors.push(errorMessage(r.reason, `${TYPE_LABELS[type]} sync failed`));
            continue;
          }
          if (r.status === 'fulfilled' && r.value.pending && r.value.ask) {
            pendingItems.push({ type, data: r.value.ask });
          }
        }

        if (pendingItems.length > 0) {
          if (!active || answeredRef.current) return;

          setPendingQueue(pendingItems);

          const current = pendingRef.current;
          const currentKey = current ? pendingKey(current) : '';
          const candidates = pendingItems.filter((item) => !answeredKeysRef.current.has(pendingKey(item)));
          const next = candidates.find((item) => pendingKey(item) === currentKey) || candidates[0];

          if (!next) {
            setPending(null);
            setCountdown(0);
            answeredRef.current = false;
            setSyncNotice('Waiting for default timeout handling to finish.');
            return;
          }

          const nextKey = pendingKey(next);
          setPending(next);
          if (currentKey !== nextKey) {
            setCountdown(initialCountdown(next));
            setSubmitError(null);
            setSyncNotice(null);
            setHitlNotes('');
            setClarifyFreeform({});
          }
          answeredRef.current = false;

          // Init clarify selections from recommended when this is a new ask.
          if (next.type === 'clarify' && currentKey !== nextKey) {
            const ask = next.data as ClarifyAsk;
            const sel: Record<string, string[]> = {};
            for (const q of Array.isArray(ask.questions) ? ask.questions : []) {
              sel[q.id] = q.recommended ? [q.recommended] : [];
            }
            setClarifySelections(sel);
          }

          return;
        }

        if (active) {
          setPending(null);
          setPendingQueue([]);
          setCountdown(0);
          answeredRef.current = false;
          answeredKeysRef.current.clear();
          setHitlNotes('');
          setClarifyFreeform({});
          if (expired.length > 0) {
            setSyncNotice(`${expired.map((t) => TYPE_LABELS[t]).join(', ')} expired; default timeout handling is active.`);
          } else if (answered) {
            setSyncNotice(null);
          } else if (syncErrors.length > 0) {
            setSyncNotice(syncErrors[0]);
          } else {
            setSyncNotice(null);
          }
        }
      } catch (err) {
        setSyncNotice(errorMessage(err, 'Could not sync pending requests.'));
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

  // ── Focus first action while the modal is open ──
  useEffect(() => {
    if (!pending) return;
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const timer = window.setTimeout(() => firstActionRef.current?.focus(), 0);
    return () => {
      window.clearTimeout(timer);
      previouslyFocused?.focus();
    };
  }, [pending ? pendingKey(pending) : '']);

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

  // ── Timeout defaults are applied by the harness waiter ──
  useEffect(() => {
    if (!pending || countdown > 0 || answeredRef.current) return;

    answeredRef.current = true;
    handleDefaultAnswer(pending);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [countdown]);

  // ── Default / recommended answer ──
  const handleDefaultAnswer = useCallback(async (p: PendingState) => {
    const key = pendingKey(p);
    answeredKeysRef.current.add(key);
    setPending(null);
    setCountdown(0);
    setSubmitError(null);
    setSyncNotice(`${TYPE_LABELS[p.type]} timed out; waiting for harness default handling.`);
    answeredRef.current = false;
  }, []);

  // ── User-driven answer ──
  const handleAnswer = useCallback(async (action: string) => {
    if (!pending || answeredRef.current) return;
    answeredRef.current = true;
    setAnswering(true);
    setSubmitError(null);
    const key = pendingKey(pending);
    const notes = hitlNotes.trim();
    const askId = metadata(pending).id;

    try {
      switch (pending.type) {
        case 'clarify': {
          const answers = Object.entries(clarifySelections).map(([qid, sel]) => ({
            question_id: qid,
            selected: sel,
            freeform: clarifyFreeform[qid]?.trim() || undefined,
          }));
          for (const [qid, freeform] of Object.entries(clarifyFreeform)) {
            if (answers.some((a) => a.question_id === qid)) continue;
            if (freeform.trim()) {
              answers.push({ question_id: qid, selected: [], freeform: freeform.trim() });
            }
          }
          // If user clicked "Use Recommended", call dedicated endpoint
          if (action === 'recommended') {
            await clarifyUseRecommended(notes, askId);
          } else {
            await answerClarify(answers, notes, askId);
          }
          break;
        }
        case 'plan':
          await approvePlan(action as 'approve' | 'replan', notes, askId);
          break;
        case 'continue':
          await answerContinue(action as 'continue' | 'stop' | 'flag_only', askId, notes);
          break;
        case 'escalate':
          await answerEscalate(action as 'retry' | 're_scope' | 'mark_done' | 'abort', askId, notes);
          break;
        case 'shell':
          await approveShell(action as 'approve' | 'deny', askId);
          break;
      }
      answeredKeysRef.current.add(key);
      setPending(null);
      setCountdown(0);
      setAnswering(false);
      answeredRef.current = false;
    } catch (err) {
      setSubmitError(errorMessage(err, 'Could not submit your answer. Check the server and try again.'));
      setAnswering(false);
      answeredRef.current = false;
    }
  }, [pending, clarifySelections, clarifyFreeform, hitlNotes]);

  // ── Toggle clarify selection ──
  const toggleClarifyOption = useCallback((questionId: string, label: string, multiSelect?: boolean) => {
    setClarifySelections((prev) => {
      const current = prev[questionId] || [];
      if (current.includes(label)) {
        return { ...prev, [questionId]: current.filter((l) => l !== label) };
      }
      if (!multiSelect) {
        return { ...prev, [questionId]: [label] };
      }
      return { ...prev, [questionId]: [...current, label] };
    });
  }, []);

  // ── Nothing to show ──
  if (!running) return null;
  if (!pending) {
    if (!syncNotice) return null;
    return (
      <div className="fixed bottom-4 right-4 z-40 max-w-sm rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 shadow-lg dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
        {syncNotice}
      </div>
    );
  }

  const color = TYPE_COLORS[pending.type];
  const timeoutSec = timeoutFor(pending);
  const progressPct = timeoutSec > 0 ? (countdown / timeoutSec) * 100 : 0;
  const urgency = countdown <= 10;
  const defaultLabel = defaultActionLabel(pending);
  const modalWidth = pending.type === 'plan' || pending.type === 'clarify' ? 'max-w-3xl' : 'max-w-lg';
  const visibleQueue = pendingQueue.filter((item) => pendingKey(item) !== pendingKey(pending));

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" />

      {/* Modal */}
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={clsx(
          'relative mx-4 flex max-h-[calc(100dvh-2rem)] w-full flex-col rounded-2xl shadow-2xl ring-1',
          'bg-white dark:bg-gray-900',
          modalWidth,
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
            <h2 id={titleId} className="text-base font-semibold text-gray-900 dark:text-gray-100">
              {TYPE_LABELS[pending.type]}
            </h2>
            <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
              {defaultLabel}
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

        {visibleQueue.length > 0 && (
          <div className="mx-6 mt-3 flex flex-wrap items-center gap-1.5">
            <span className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
              Also waiting
            </span>
            {visibleQueue.map((item) => (
              <span
                key={pendingKey(item)}
                className="rounded-full border border-gray-200 bg-gray-50 px-2 py-0.5 text-[10px] font-medium text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400"
              >
                {TYPE_LABELS[item.type]}
              </span>
            ))}
          </div>
        )}

        {/* Body — type-specific */}
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          {pending.type === 'clarify' && (
            <ClarifyBody
              data={pending.data as ClarifyAsk}
              selections={clarifySelections}
              freeform={clarifyFreeform}
              onToggle={toggleClarifyOption}
              onFreeformChange={(questionId, value) =>
                setClarifyFreeform((prev) => ({ ...prev, [questionId]: value }))
              }
            />
          )}
          {pending.type === 'plan' && <PlanBody data={pending.data as PlanAsk} />}
          {pending.type === 'continue' && <ContinueBody data={pending.data as ContinueAsk} />}
          {pending.type === 'escalate' && <EscalateBody data={pending.data as EscalateAsk} />}
          {pending.type === 'shell' && <ShellBody data={pending.data as ShellAsk} />}
        </div>

        {submitError && (
          <div className="mx-6 mb-4 px-3 py-2 rounded-lg border border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-900/20 text-xs text-red-700 dark:text-red-300">
            {submitError}
          </div>
        )}

        {/* Actions */}
        <div className="px-6 pb-5 space-y-2">
          {pending.type === 'clarify' && (
            <ClarifyActions
              color={color}
              answering={answering}
              notes={hitlNotes}
              onNotesChange={setHitlNotes}
              firstActionRef={firstActionRef}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'plan' && (
            <PlanActions
              color={color}
              answering={answering}
              notes={hitlNotes}
              onNotesChange={setHitlNotes}
              firstActionRef={firstActionRef}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'continue' && (
            <ContinueActions
              color={color}
              answering={answering}
              notes={hitlNotes}
              onNotesChange={setHitlNotes}
              firstActionRef={firstActionRef}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'escalate' && (
            <EscalateActions
              color={color}
              answering={answering}
              notes={hitlNotes}
              onNotesChange={setHitlNotes}
              firstActionRef={firstActionRef}
              onAnswer={handleAnswer}
            />
          )}
          {pending.type === 'shell' && (
            <ShellActions
              color={color}
              answering={answering}
              firstActionRef={firstActionRef}
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
  freeform,
  onToggle,
  onFreeformChange,
}: {
  data: ClarifyAsk;
  selections: Record<string, string[]>;
  freeform: Record<string, string>;
  onToggle: (questionId: string, label: string, multiSelect?: boolean) => void;
  onFreeformChange: (questionId: string, value: string) => void;
}) {
  const draft = data.prd_draft;
  const questions = Array.isArray(data.questions) ? data.questions : [];
  return (
    <div className="space-y-4">
      {data.query && (
        <div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/60 px-3 py-2.5">
          <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Request
          </div>
          <p className="mt-1 text-xs text-gray-700 dark:text-gray-300 break-words">{data.query}</p>
        </div>
      )}

      {draft && (draft.summary || draft.goals?.length || draft.acceptance?.length || draft.constraints?.length) && (
        <div className="grid gap-3 md:grid-cols-2">
          {draft.summary && (
            <div>
              <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
                Draft Scope
              </div>
              <p className="mt-1 text-xs text-gray-700 dark:text-gray-300 break-words">{draft.summary}</p>
            </div>
          )}
          {draft.language || draft.entrypoint ? (
            <div>
              <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
                Stack
              </div>
              <p className="mt-1 text-xs text-gray-700 dark:text-gray-300">
                {[draft.language, draft.entrypoint].filter(Boolean).join(' · ')}
              </p>
            </div>
          ) : null}
          <CompactList title="Goals" items={draft.goals || []} />
          <CompactList title="Acceptance" items={draft.acceptance || []} />
          <CompactList title="Constraints" items={draft.constraints || []} />
        </div>
      )}

      {questions.length === 0 && (
        <div className="rounded-lg border border-purple-200 dark:border-purple-800 bg-purple-50/70 dark:bg-purple-900/20 px-3 py-2.5">
          <p className="text-xs text-purple-800 dark:text-purple-200">
            The specification ask did not include structured questions. Add notes below or use recommended defaults.
          </p>
        </div>
      )}

      {questions.map((q: ClarifyAsk['questions'][number], index: number) => {
        const questionId = q.id || `q${index + 1}`;
        const options = Array.isArray(q.options) ? q.options : [];
        const showFreeform = q.allow_freeform || options.length === 0;
        return (
          <div key={questionId}>
            <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-1">
              {q.header || `Question ${index + 1}`}
            </h3>
            <p className="text-xs text-gray-500 dark:text-gray-400 mb-2">
              {q.question}
            </p>
            <div className="space-y-1.5">
              {options.map((opt: typeof q.options[number]) => {
                const checked = (selections[questionId] || []).includes(opt.label);
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
                      type={q.multi_select ? 'checkbox' : 'radio'}
                      name={questionId}
                      checked={checked}
                      onChange={() => onToggle(questionId, opt.label, q.multi_select)}
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
              {showFreeform && (
                <textarea
                  value={freeform[questionId] || ''}
                  onChange={(e) => onFreeformChange(questionId, e.target.value)}
                  rows={2}
                  aria-label={`${q.header || questionId} freeform answer`}
                  placeholder="Add a specific answer"
                  className="w-full resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-purple-500/30"
                />
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function PlanBody({ data }: { data: PlanAsk }) {
  const goals = data.goals || [];
  const assumptions = data.assumptions || [];
  const tasks = data.tasks || [];
  const taskDetails = data.task_details || [];
  const validation = data.validation;
  const composition = data.composition;
  return (
    <div className="space-y-4">
      {data.query && (
        <div className="rounded-lg border border-fuchsia-200 dark:border-fuchsia-800 bg-fuchsia-50/70 dark:bg-fuchsia-900/20 px-3 py-2.5">
          <div className="text-[10px] font-semibold uppercase text-fuchsia-500 dark:text-fuchsia-300">
            Request
          </div>
          <p className="mt-1 text-sm text-gray-800 dark:text-gray-200 break-words">{data.query}</p>
        </div>
      )}

      <div>
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Plan Summary</h3>
          <div className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
            <ClipboardList className="w-3.5 h-3.5" />
            <span>
              {data.task_count} task{data.task_count !== 1 ? 's' : ''}
            </span>
          </div>
        </div>
        {data.summary && (
          <p className="mt-1.5 text-sm text-gray-700 dark:text-gray-300 break-words">{data.summary}</p>
        )}
      </div>

      {(goals.length > 0 || assumptions.length > 0) && (
        <div className="grid gap-3 md:grid-cols-2">
          <CompactList title="Goals" items={goals} />
          <CompactList title="Assumptions" items={assumptions} />
        </div>
      )}

      {validation && (
        <div
          className={clsx(
            'rounded-lg border px-3 py-2.5',
            validation.ok
              ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-800 dark:bg-emerald-900/20'
              : 'border-amber-200 bg-amber-50 dark:border-amber-800 dark:bg-amber-900/20',
          )}
        >
          <div
            className={clsx(
              'text-[10px] font-semibold uppercase',
              validation.ok
                ? 'text-emerald-600 dark:text-emerald-300'
                : 'text-amber-600 dark:text-amber-300',
            )}
          >
            {validation.ok ? 'Validation Passed' : 'Validation Needs Review'}
          </div>
          {!validation.ok && (
            <div className="mt-2 grid gap-3 md:grid-cols-2">
              <CompactList title="Issues" items={validation.issues || []} />
              <CompactList title="Hints" items={validation.hints || []} />
              <CompactList title="Weak Tasks" items={validation.weak_task_ids || []} />
            </div>
          )}
        </div>
      )}

      {composition && (
        <CompositionApprovalPanel composition={composition} />
      )}

      {taskDetails.length > 0 ? (
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Execution Tasks
          </div>
          <div className="space-y-2">
            {taskDetails.map((task: PlanApprovalTask, i: number) => (
              <div
                key={`${task.id || 'task'}-${i}`}
                className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2.5"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <span className="rounded bg-fuchsia-100 dark:bg-fuchsia-900/40 px-1.5 py-0.5 text-[10px] font-semibold text-fuchsia-700 dark:text-fuchsia-300">
                    {task.id || `T${i + 1}`}
                  </span>
                  {task.role && (
                    <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:text-gray-400">
                      {task.role}
                    </span>
                  )}
                  {task.priority ? (
                    <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:text-gray-400">
                      P{task.priority}
                    </span>
                  ) : null}
                  {task.depends_on && task.depends_on.length > 0 && (
                    <span className="text-[10px] text-gray-400 dark:text-gray-500">
                      after {task.depends_on.join(', ')}
                    </span>
                  )}
                </div>
                <div className="mt-2 text-sm font-medium text-gray-800 dark:text-gray-200 break-words">
                  {task.title}
                </div>
                {task.description && (
                  <p className="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400 break-words">
                    {task.description}
                  </p>
                )}
                {task.files && task.files.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {task.files.slice(0, 6).map((file) => (
                      <span
                        key={file}
                        className="rounded border border-gray-200 dark:border-gray-700 px-1.5 py-0.5 text-[10px] font-mono text-gray-500 dark:text-gray-400"
                      >
                        {file}
                      </span>
                    ))}
                    {task.files.length > 6 && (
                      <span className="text-[10px] text-gray-400 dark:text-gray-500">
                        +{task.files.length - 6} files
                      </span>
                    )}
                  </div>
                )}
                {task.acceptance && (
                  <div className="mt-2 rounded bg-gray-50 dark:bg-gray-800/70 px-2.5 py-2 text-xs leading-relaxed text-gray-600 dark:text-gray-300 break-words">
                    {task.acceptance}
                  </div>
                )}
              </div>
            ))}
          </div>
          {data.task_count > taskDetails.length && (
            <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
              Showing {taskDetails.length} of {data.task_count} tasks.
            </p>
          )}
        </div>
      ) : tasks.length > 0 ? (
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Execution Tasks
          </div>
          <div className="space-y-2">
            {tasks.map((t: string, i: number) => {
              const match = t.match(/^([^:]+):\s*(.*)$/);
              const taskID = match?.[1] || `T${i + 1}`;
              const body = match?.[2] || t;
              return (
                <div
                  key={`${taskID}-${i}`}
                  className="flex items-start gap-2.5 rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2.5"
                >
                  <span className="shrink-0 rounded bg-fuchsia-100 dark:bg-fuchsia-900/40 px-1.5 py-0.5 text-[10px] font-semibold text-fuchsia-700 dark:text-fuchsia-300">
                    {taskID}
                  </span>
                  <span className="text-xs leading-relaxed text-gray-700 dark:text-gray-300 break-words">
                    {body}
                  </span>
                </div>
              );
            })}
          </div>
          {data.task_count > tasks.length && (
            <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
              Showing {tasks.length} of {data.task_count} tasks.
            </p>
          )}
        </div>
      ) : null}
    </div>
  );
}

function ContinueBody({ data }: { data: ContinueAsk }) {
  const escalated = data.escalated || [];
  const gaps = data.gaps || [];
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700 dark:text-gray-300">{data.summary}</p>
      {data.reason && (
        <div className="flex items-start gap-2 p-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800">
          <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0 mt-0.5" />
          <p className="text-xs text-amber-700 dark:text-amber-300">{data.reason}</p>
        </div>
      )}
      {escalated.length > 0 && (
        <div>
          <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">
            Escalated tasks:
          </p>
          {escalated.map((t: string, i: number) => (
            <div key={i} className="text-xs text-red-600 dark:text-red-400 ml-2">
              • {t}
            </div>
          ))}
        </div>
      )}
      {gaps.length > 0 && (
        <div>
          <p className="text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">
            Gaps found:
          </p>
          {gaps.map((g: string, i: number) => (
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
  const files = data.files || [];
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
        {files.length > 0 && (
          <span className="text-[10px] text-gray-400 dark:text-gray-500">
            Files: {files.join(', ')}
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
  notes,
  onNotesChange,
  firstActionRef,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  notes: string;
  onNotesChange: (notes: string) => void;
  firstActionRef: RefObject<HTMLButtonElement>;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="space-y-2">
      <textarea
        value={notes}
        onChange={(e) => onNotesChange(e.target.value)}
        rows={2}
        aria-label="Scope notes"
        placeholder="Notes for scope or constraints"
        className="w-full resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-purple-500/30"
      />
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <button
          ref={firstActionRef}
          disabled={answering}
          onClick={() => onAnswer('submit')}
          className={clsx(
            'flex flex-1 items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-sm font-semibold transition-all',
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
            'rounded-xl border px-4 py-2.5 text-sm font-medium transition-all',
            COLOR_BTN_GHOST[color],
            'disabled:opacity-50',
          )}
        >
          Use Recommended
        </button>
      </div>
    </div>
  );
}

function PlanActions({
  color,
  answering,
  notes,
  onNotesChange,
  firstActionRef,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  notes: string;
  onNotesChange: (notes: string) => void;
  firstActionRef: RefObject<HTMLButtonElement>;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="space-y-2">
      <textarea
        value={notes}
        onChange={(e) => onNotesChange(e.target.value)}
        rows={2}
        aria-label="Plan notes"
        placeholder="Notes, constraints, or replan instructions"
        className="w-full resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-fuchsia-500/30"
      />
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <button
          ref={firstActionRef}
          disabled={answering}
          onClick={() => onAnswer('approve')}
          className={clsx(
            'flex flex-1 items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-sm font-semibold transition-all',
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
            'flex items-center justify-center gap-2 rounded-xl border px-4 py-2.5 text-sm font-medium transition-all',
            COLOR_BTN_GHOST[color],
            'disabled:opacity-50',
          )}
        >
          <RefreshCw className="w-4 h-4" />
          Stop for Replan
        </button>
      </div>
    </div>
  );
}

function ContinueActions({
  color,
  answering,
  notes,
  onNotesChange,
  firstActionRef,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  notes: string;
  onNotesChange: (notes: string) => void;
  firstActionRef: RefObject<HTMLButtonElement>;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="space-y-2">
      <textarea
        value={notes}
        onChange={(e) => onNotesChange(e.target.value)}
        rows={2}
        aria-label="Continue notes"
        placeholder="Guidance for the next wave"
        className="w-full resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-amber-500/30"
      />
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <button
          ref={firstActionRef}
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
          aria-label="Stop"
          title="Stop"
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
          title="Keep precise flags"
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
    </div>
  );
}

function EscalateActions({
  color,
  answering,
  notes,
  onNotesChange,
  firstActionRef,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  notes: string;
  onNotesChange: (notes: string) => void;
  firstActionRef: RefObject<HTMLButtonElement>;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="space-y-2">
      <textarea
        value={notes}
        onChange={(e) => onNotesChange(e.target.value)}
        rows={2}
        aria-label="Escalation notes"
        placeholder="Retry, re-scope, or override guidance"
        className="w-full resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-red-500/30"
      />
      <div className="flex items-center gap-2">
        <button
          ref={firstActionRef}
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
  firstActionRef,
  onAnswer,
}: {
  color: string;
  answering: boolean;
  firstActionRef: RefObject<HTMLButtonElement>;
  onAnswer: (action: string) => void;
}) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
      <button
        ref={firstActionRef}
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
        title="Deny command"
        aria-label="Deny command"
        disabled={answering}
        onClick={() => onAnswer('deny')}
        className={clsx(
          'px-4 py-2.5 rounded-xl text-sm font-medium border transition-all',
          'border-red-300 dark:border-red-800 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30',
          'disabled:opacity-50',
        )}
      >
        <X className="w-4 h-4" />
        Deny
      </button>
    </div>
  );
}

// ── Helpers ──

function CompositionApprovalPanel({ composition }: { composition: DynamicComposition }) {
  const phases = (composition.phases || []).filter((phase) => phase.enabled && phase.when !== 'never');
  const team = composition.team || [];
  const slots = composition.slots || [];
  const loop = composition.execute || {};
  return (
    <div className="rounded-lg border border-indigo-200 bg-indigo-50/70 px-3 py-2.5 dark:border-indigo-800 dark:bg-indigo-900/20">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-[10px] font-semibold uppercase text-indigo-600 dark:text-indigo-300">
            Selected Pipeline
          </div>
          {composition.summary && (
            <p className="mt-1 text-sm font-medium text-gray-800 dark:text-gray-200 break-words">
              {composition.summary}
            </p>
          )}
        </div>
        <div className="flex flex-wrap gap-1">
          {loop.default_role && (
            <MiniBadge label={`worker ${loop.default_role}`} />
          )}
          {loop.reviewer && (
            <MiniBadge label={`review ${loop.reviewer}`} />
          )}
          {loop.corrector && (
            <MiniBadge label={`fix ${loop.corrector}`} />
          )}
          {typeof loop.max_waves === 'number' && loop.max_waves > 0 && (
            <MiniBadge label={`${loop.max_waves} wave${loop.max_waves === 1 ? '' : 's'}`} />
          )}
        </div>
      </div>

      {composition.strategy && (
        <p className="mt-2 text-xs leading-relaxed text-gray-600 dark:text-gray-400 break-words">
          {composition.strategy}
        </p>
      )}

      {composition.handoff && composition.handoff.length > 0 && (
        <div className="mt-3">
          <CompactList title="Handoff" items={composition.handoff} maxItems={composition.handoff.length} />
        </div>
      )}

      {phases.length > 0 && (
        <div className="mt-3">
          <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Phases
          </div>
          <div className="mt-1.5 flex flex-wrap gap-1">
            {phases.map((phase) => (
              <span
                key={phase.id}
                className="rounded border border-indigo-200 bg-white/70 px-1.5 py-0.5 text-[10px] text-gray-600 dark:border-indigo-700 dark:bg-gray-950/40 dark:text-gray-300"
              >
                {phase.id}
                {phase.agent ? ` @${phase.agent}` : ''}
              </span>
            ))}
          </div>
        </div>
      )}

      {team.length > 0 && (
        <div className="mt-3">
          <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Team
          </div>
          <div className="mt-1.5 grid gap-1.5 md:grid-cols-2">
            {team.map((member) => (
              <div
                key={member.role}
                className="rounded border border-indigo-200 bg-white/70 px-2 py-1.5 text-xs text-gray-600 dark:border-indigo-700 dark:bg-gray-950/40 dark:text-gray-300"
              >
                <span className="font-semibold">@{member.role}</span>
                {member.skills && member.skills.length > 0 && (
                  <span className="text-gray-400 dark:text-gray-500">
                    {' '}
                    {member.skills.join(', ')}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {slots.length > 0 && (
        <div className="mt-3">
          <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
            Slots
          </div>
          <div className="mt-1.5 space-y-1">
            {slots.map((slot) => (
              <div key={slot.id} className="text-xs text-gray-600 dark:text-gray-400">
                <span className="font-medium text-gray-700 dark:text-gray-300">{slot.id}</span>
                {slot.agent ? ` @${slot.agent}` : ''}
                {' '}
                <span className="text-gray-400 dark:text-gray-500">
                  {slot.before ? `before ${slot.before}` : slot.after ? `after ${slot.after}` : slot.replace ? `replace ${slot.replace}` : 'slot'}
                  {slot.persist_to ? ` · ${slot.persist_to}` : ''}
                  {slot.fail_mode ? ` · ${slot.fail_mode}` : ''}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function MiniBadge({ label }: { label: string }) {
  return (
    <span className="rounded bg-white/80 px-1.5 py-0.5 text-[10px] font-medium text-indigo-700 dark:bg-gray-950/50 dark:text-indigo-300">
      {label}
    </span>
  );
}

function CompactList({ title, items, maxItems = 6 }: { title: string; items: string[]; maxItems?: number }) {
  if (!items.length) return null;
  const visibleItems = items.slice(0, maxItems);
  return (
    <div>
      <div className="text-[10px] font-semibold uppercase text-gray-400 dark:text-gray-500">
        {title}
      </div>
      <div className="mt-1.5 space-y-1.5">
        {visibleItems.map((item, i) => (
          <div key={`${title}-${i}`} className="flex items-start gap-1.5 text-xs text-gray-600 dark:text-gray-400">
            <ChevronRight className="mt-0.5 h-3 w-3 shrink-0 text-gray-400" />
            <span className="break-words">{item}</span>
          </div>
        ))}
      </div>
      {items.length > maxItems && (
        <div className="mt-1 text-xs text-gray-400 dark:text-gray-500">
          +{items.length - maxItems} more
        </div>
      )}
    </div>
  );
}

function metadata(p: PendingState): AskMetadata {
  return p.data as AskMetadata;
}

function pendingKey(p: PendingState): string {
  const m = metadata(p);
  return `${p.type}:${m.id || m.created_at || JSON.stringify(p.data).slice(0, 120)}`;
}

function timeoutFor(p: PendingState): number {
  const t = Number(metadata(p).timeout_sec);
  return Number.isFinite(t) && t > 0 ? t : TIMEOUTS[p.type];
}

function initialCountdown(p: PendingState): number {
  const timeout = timeoutFor(p);
  const createdAt = metadata(p).created_at;
  if (!createdAt) return timeout;
  const createdMs = Date.parse(createdAt);
  if (!Number.isFinite(createdMs)) return timeout;
  const elapsed = Math.floor((Date.now() - createdMs) / 1000);
  return Math.max(0, Math.min(timeout, timeout - elapsed));
}

function defaultAction(p: PendingState): string {
  const fromAsk = (metadata(p).on_timeout || '').trim();
  if (fromAsk) {
    switch (fromAsk) {
      case 'approve':
        return p.type === 'shell' ? 'approve' : 'approve';
      case 'use_recommended':
      case 'recommended':
        return 'recommended';
      case 'stop':
      case 'flag_only':
      case 'continue':
      case 'deny':
      case 're_scope':
      case 'retry':
      case 'mark_done':
      case 'abort':
        return fromAsk;
      case 'slm':
        return 'none';
      default:
        return fromAsk;
    }
  }
  switch (p.type) {
    case 'clarify':
      return 'recommended';
    case 'plan':
      return 'approve';
    case 'continue':
      return 'stop';
    case 'escalate':
      return 'none';
    case 'shell':
      return 'deny';
  }
}

function defaultActionLabel(p: PendingState): string {
  const action = defaultAction(p);
  if (action === 'none') return 'Default: harness decides on timeout';
  const labels: Record<string, string> = {
    approve: 'approve',
    recommended: 'use recommended answers',
    stop: 'stop and keep flags',
    flag_only: 'keep precise flags',
    continue: 'continue',
    deny: 'deny',
    re_scope: 'send back to scope',
    retry: 'retry',
    mark_done: 'mark done',
    abort: 'abort',
  };
  return `Default: ${labels[action] || action} on timeout`;
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

function formatSeconds(total: number): string {
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}
