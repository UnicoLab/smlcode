// ── App-wide window events ──
//
// A few interactions cross component boundaries that do not share a parent:
// a keyboard shortcut in the layout wants to run the prompt that lives in the
// Live view; a task card on the board wants to prefill the feedback box at the
// bottom of the rail. A CustomEvent on `window` is the smallest thing that
// works, and every event name lives here so the contract is in one place.
//
// Events are dispatched `cancelable`. A listener that handles one calls
// `preventDefault()`, and `dispatchEvent` then returns false — which lets the
// dispatcher fall back to doing the work itself when nobody is listening.

/** Fired on `/`: the Live view focuses its prompt. No detail. */
export const FOCUS_PROMPT_EVENT = 'slmcode:focus-prompt';

/**
 * Fired on Cmd/Ctrl+Enter while the prompt is focused, and by "Run again"
 * on the result panel. `detail.query` is set when the dispatcher wants a
 * specific prompt run; absent means "run whatever is in the prompt box".
 * The Live view should call preventDefault() once it has taken the request.
 */
export const RUN_PROMPT_EVENT = 'slmcode:run-prompt';

/** Fired on Cmd/Ctrl+. while the prompt is focused: stop the active run. */
export const STOP_RUN_EVENT = 'slmcode:stop-run';

/**
 * Fired by a task's "Steer" action: LiveFeedback prefills its composer with
 * `@task:<id> ` and focuses it. `detail.taskId` is the task id.
 */
export const STEER_TASK_EVENT = 'slmcode:steer-task';

/** Fired on Cmd/Ctrl+K: toggle the command palette. */
export const COMMAND_PALETTE_EVENT = 'slmcode:command-palette';

export interface RunPromptDetail {
  query?: string;
}

export interface SteerTaskDetail {
  taskId: string;
}

/** Dispatch and report whether a listener claimed the event. */
export function emit<T>(name: string, detail?: T): boolean {
  const ev = new CustomEvent<T | undefined>(name, { detail, cancelable: true });
  return !window.dispatchEvent(ev);
}

/** Subscribe with a typed detail; returns the unsubscribe function. */
export function on<T>(name: string, handler: (detail: T, ev: CustomEvent<T>) => void): () => void {
  const listener = (ev: Event) => {
    const custom = ev as CustomEvent<T>;
    handler(custom.detail, custom);
  };
  window.addEventListener(name, listener);
  return () => window.removeEventListener(name, listener);
}

const STEER_KEY = 'slmcode:steer';

/**
 * Ask LiveFeedback to prefill a steer for one task. When no composer is
 * mounted (the board page, say) the request is parked in sessionStorage and
 * the caller navigates to Live, where the composer picks it up on mount.
 * Returns whether a mounted composer took it.
 */
export function steerTask(taskId: string): boolean {
  if (emit<SteerTaskDetail>(STEER_TASK_EVENT, { taskId })) return true;
  try {
    sessionStorage.setItem(STEER_KEY, taskId);
  } catch {
    /* private mode — the user can still type the tag by hand */
  }
  return false;
}

/** Consume a parked steer request, if any. */
export function takePendingSteer(): string | null {
  try {
    const id = sessionStorage.getItem(STEER_KEY);
    if (id) sessionStorage.removeItem(STEER_KEY);
    return id;
  } catch {
    return null;
  }
}

/** The prefix LiveFeedback prefills for a steer. */
export function steerPrefix(taskId: string): string {
  return `@task:${taskId} `;
}
