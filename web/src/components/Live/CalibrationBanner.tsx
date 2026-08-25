import { useMemo } from 'react';
import { Ruler } from 'lucide-react';
import type { RunEvent } from '@/types';

// Calibration progress, while it is happening.
//
// A model that has never been measured is measured on its first run, and for a
// cold 42GB local model that can take a minute of weight-loading before a
// single token is produced. Without this the user sees a Run button that
// appears to do nothing — the single worst moment in the product, because the
// natural response is to click Run again.
//
// The banner reads the SAME events the log does (kind "calibration", emitted by
// the server's ensureCalibrated progress callback), so there is no second
// source of truth about whether calibration is running.
export default function CalibrationBanner({ events }: { events: RunEvent[] }) {
  const state = useMemo(() => calibrationProgress(events), [events]);
  if (!state) return null;

  return (
    <div className="mb-3 rounded-lg border border-brand-200 bg-brand-50 px-4 py-3 dark:border-brand-900 dark:bg-brand-950/30">
      <div className="flex items-center gap-3">
        <Ruler size={18} className="shrink-0 animate-pulse text-brand-600" />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-sm font-semibold">Calibrating model</span>
            {state.total > 0 && (
              <span className="shrink-0 text-xs tabular-nums text-gray-500">
                {state.step}/{state.total}
              </span>
            )}
          </div>
          <p className="truncate text-xs text-gray-600 dark:text-gray-400" title={state.detail}>
            {state.detail}
          </p>
        </div>
      </div>
      {state.total > 0 && (
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-brand-100 dark:bg-brand-900/50">
          <div
            className="h-full rounded-full bg-brand-500 transition-all duration-500"
            style={{ width: `${Math.min(100, Math.round((state.step / state.total) * 100))}%` }}
          />
        </div>
      )}
    </div>
  );
}

export interface CalibrationProgressState {
  step: number;
  total: number;
  detail: string;
}

// calibrationProgress returns the live state, or null when calibration is not
// the thing currently happening.
//
// The visibility rule is "no event has moved past init since calibration last
// spoke". Keying off a completion event instead would leave the banner stuck on
// screen whenever calibration fails or is cancelled — the two cases where a
// stuck progress bar is most misleading.
export function calibrationProgress(events: RunEvent[]): CalibrationProgressState | null {
  let lastCal = -1;
  let lastNonInit = -1;
  events.forEach((e, i) => {
    if (e.kind === 'calibration') lastCal = i;
    if (e.phase && e.phase !== 'init') lastNonInit = i;
  });
  if (lastCal < 0 || lastCal < lastNonInit) return null;

  const message = String(events[lastCal].message || '').trim();
  if (!message) return null;

  // Progress.String() renders "stage (step/total) — detail".
  const m = message.match(/^(.*?)\s*\((\d+)\/(\d+)\)\s*(?:—\s*(.*))?$/);
  if (!m) return { step: 0, total: 0, detail: message };
  const detail = (m[4] || '').trim();
  return {
    step: Number(m[2]),
    total: Number(m[3]),
    detail: detail ? `${m[1].trim()} — ${detail}` : m[1].trim(),
  };
}
