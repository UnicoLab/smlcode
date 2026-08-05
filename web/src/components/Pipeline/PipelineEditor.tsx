import { useState, useEffect, useCallback } from 'react';
import { getPipeline, updatePipeline, resetPipeline } from '@/api/client';
import type { PipelineView, PhaseSpec } from '@/types';
import {
  Play,
  Pause,
  SkipForward,
  RotateCcw,
  Save,
  GripVertical,
  Check,
  X,
} from 'lucide-react';
import clsx from 'clsx';

const WHEN_COLORS: Record<string, string> = {
  always: 'text-emerald-500',
  auto: 'text-amber-500',
  never: 'text-gray-400 line-through',
};

const GROUP_COLORS: Record<string, string> = {
  prepare: 'bg-sky-100 dark:bg-sky-900/30 text-sky-700 dark:text-sky-300',
  design: 'bg-violet-100 dark:bg-violet-900/30 text-violet-700 dark:text-violet-300',
  build: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
  verify: 'bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300',
  finish: 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400',
};

export default function PipelineEditor() {
  const [pipeline, setPipeline] = useState<PipelineView | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const fetch = useCallback(async () => {
    try {
      const p = await getPipeline();
      setPipeline(p);
    } catch (e) {
      console.error('Failed to load pipeline:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetch();
  }, [fetch]);

  const handleTogglePhase = async (phaseId: string, enabled: boolean) => {
    if (!pipeline) return;
    const newPhases = {
      ...pipeline.config.phases,
      [phaseId]: { ...pipeline.config.phases[phaseId], enabled },
    };
    setPipeline({
      ...pipeline,
      config: { ...pipeline.config, phases: newPhases },
    });
  };

  const handleAgentChange = (phaseId: string, agent: string) => {
    if (!pipeline) return;
    const newPhases = {
      ...pipeline.config.phases,
      [phaseId]: { ...pipeline.config.phases[phaseId], agent },
    };
    setPipeline({
      ...pipeline,
      config: { ...pipeline.config, phases: newPhases },
    });
  };

  const handleWhenChange = (phaseId: string, when: string) => {
    if (!pipeline) return;
    const newPhases = {
      ...pipeline.config.phases,
      [phaseId]: { ...pipeline.config.phases[phaseId], when },
    };
    setPipeline({
      ...pipeline,
      config: { ...pipeline.config, phases: newPhases },
    });
  };

  const handleSave = async () => {
    if (!pipeline) return;
    setSaving(true);
    try {
      await updatePipeline(pipeline.config);
    } catch (e) {
      console.error('Save failed:', e);
    } finally {
      setSaving(false);
    }
  };

  const handleReset = async () => {
    try {
      const p = await resetPipeline();
      setPipeline(p);
    } catch (e) {
      console.error('Reset failed:', e);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="flex items-center gap-3 text-gray-400">
          <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin" />
          Loading pipeline…
        </div>
      </div>
    );
  }

  if (!pipeline) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400">
        No pipeline data.
      </div>
    );
  }

  const { config } = pipeline;
  const groups = config.groups || [];

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-3xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Pipeline</h1>
            <p className="text-sm text-gray-500 mt-1">Configure the agent execution pipeline phases</p>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={handleReset} className="btn-ghost text-sm gap-2">
              <RotateCcw size={16} />
              Reset
            </button>
            <button onClick={handleSave} disabled={saving} className="btn-primary text-sm gap-2">
              <Save size={16} />
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>

        {/* Execute loop config */}
        <div className="card p-4 grid grid-cols-3 gap-4">
          <div>
            <div className="label">Default Role</div>
            <div className="text-sm font-mono">{config.execute?.default_role || 'worker'}</div>
          </div>
          <div>
            <div className="label">Reviewer</div>
            <div className="text-sm font-mono">{config.execute?.reviewer || 'reviewer'}</div>
          </div>
          <div>
            <div className="label">Max Waves</div>
            <div className="text-sm font-mono">{config.execute?.max_waves || 2}</div>
          </div>
        </div>

        {/* Phase groups */}
        {groups.map((group) => {
          const phases = group.steps
            .filter((id) => config.phases[id])
            .map((id) => ({ id, ...config.phases[id] }));

          if (phases.length === 0) return null;

          return (
            <div key={group.id} className="card overflow-hidden">
              {/* Group header */}
              <div className={clsx('px-4 py-2 text-xs font-bold', GROUP_COLORS[group.id] || 'bg-gray-50 dark:bg-gray-800')}>
                {group.label}
              </div>

              {/* Phases */}
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {phases.map((phase) => {
                  const isEnabled = phase.enabled !== false;
                  const defaultAgent = pipeline.defaults?.[phase.id] || '';
                  return (
                    <div
                      key={phase.id}
                      className={clsx(
                        'flex items-center gap-4 px-4 py-3 transition-colors',
                        !isEnabled && 'opacity-50',
                      )}
                    >
                      {/* Drag handle */}
                      <button className="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 cursor-grab">
                        <GripVertical size={16} />
                      </button>

                      {/* Phase name */}
                      <div className="w-28 shrink-0">
                        <div className="text-sm font-bold">{phase.label || phase.id}</div>
                        <div className="text-[10px] text-gray-400 font-mono">{phase.id}</div>
                      </div>

                      {/* Tip */}
                      <div className="flex-1 min-w-0">
                        <div className="text-xs text-gray-500 truncate">{phase.tip}</div>
                      </div>

                      {/* Agent */}
                      <input
                        type="text"
                        value={phase.agent || ''}
                        onChange={(e) => handleAgentChange(phase.id, e.target.value)}
                        placeholder={defaultAgent || 'auto'}
                        className="input w-28 text-xs font-mono py-1.5"
                        disabled={!isEnabled}
                      />

                      {/* When */}
                      <select
                        value={phase.when || 'auto'}
                        onChange={(e) => handleWhenChange(phase.id, e.target.value)}
                        className={clsx('input w-20 text-xs py-1.5', WHEN_COLORS[phase.when || 'auto'])}
                        disabled={!isEnabled}
                      >
                        <option value="always">Always</option>
                        <option value="auto">Auto</option>
                        <option value="never">Never</option>
                      </select>

                      {/* Toggle */}
                      <button
                        onClick={() => handleTogglePhase(phase.id, !isEnabled)}
                        className={clsx(
                          'p-1.5 rounded-lg transition-colors',
                          isEnabled
                            ? 'text-emerald-500 hover:bg-emerald-50 dark:hover:bg-emerald-900/20'
                            : 'text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800',
                        )}
                        title={isEnabled ? 'Disable' : 'Enable'}
                      >
                        {isEnabled ? <Check size={16} /> : <X size={16} />}
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
