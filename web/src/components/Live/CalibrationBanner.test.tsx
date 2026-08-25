import { describe, expect, it } from 'vitest';
import { calibrationProgress } from './CalibrationBanner';
import type { RunEvent } from '@/types';

const ev = (e: Partial<RunEvent>): RunEvent => ({ phase: '', kind: '', message: '', time: '', ...e });

describe('calibrationProgress', () => {
  it('parses the stage, step and detail out of Progress.String()', () => {
    const got = calibrationProgress([
      ev({ phase: 'init', kind: 'calibration', message: 'context window (2/4) — probing 262144' }),
    ]);
    expect(got).toEqual({ step: 2, total: 4, detail: 'context window — probing 262144' });
  });

  it('handles a stage with no step count', () => {
    const got = calibrationProgress([
      ev({ phase: 'init', kind: 'calibration', message: 'loading model weights' }),
    ]);
    expect(got).toEqual({ step: 0, total: 0, detail: 'loading model weights' });
  });

  // The banner must disappear on its own. Keying off a completion event would
  // leave it on screen forever whenever calibration failed or was cancelled —
  // exactly when a frozen progress bar is most misleading.
  it('hides once the run moves past init', () => {
    const events = [
      ev({ phase: 'init', kind: 'calibration', message: 'concurrency knee (3/4)' }),
      ev({ phase: 'plan', kind: 'agent_start', message: 'coordinator' }),
    ];
    expect(calibrationProgress(events)).toBeNull();
  });

  // A second model switched mid-session calibrates again, and the banner has to
  // come back for it rather than staying hidden because a run already happened.
  it('reappears when calibration speaks again after a run', () => {
    const events = [
      ev({ phase: 'init', kind: 'calibration', message: 'latency baseline (1/4)' }),
      ev({ phase: 'done', kind: 'run_done', message: 'finished' }),
      ev({ phase: 'init', kind: 'calibration', message: 'latency baseline (1/4) — new model' }),
    ];
    expect(calibrationProgress(events)?.detail).toBe('latency baseline — new model');
  });

  it('stays hidden when nothing has calibrated', () => {
    expect(calibrationProgress([])).toBeNull();
    expect(calibrationProgress([ev({ phase: 'plan', kind: 'agent_start' })])).toBeNull();
  });

  // An empty message would render an empty banner, which reads as a hang.
  it('ignores a calibration event with no message', () => {
    expect(calibrationProgress([ev({ phase: 'init', kind: 'calibration', message: '' })])).toBeNull();
  });
});
