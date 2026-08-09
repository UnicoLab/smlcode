import { useState, useEffect, useCallback, useContext } from 'react';
import { getPipeline, updatePipeline, resetPipeline, getBlocks, applyPipelinePreset } from '@/api/client';
import { AppContext } from '@/App';
import type { PipelineView, BlockView } from '@/types';
import {
  Play,
  RotateCcw,
  Save,
  GripVertical,
  Check,
  X,
  Layers,
  Zap,
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
  const ctx = useContext(AppContext);
  const [pipeline, setPipeline] = useState<PipelineView | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [presets, setPresets] = useState<any[]>([]);
  const [applyingPreset, setApplyingPreset] = useState(false);
  const [presetNotice, setPresetNotice] = useState<string | null>(null);

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

  const fetchPresets = useCallback(async () => {
    try {
      const data = await getBlocks('pipeline');
      setPresets(data.blocks?.filter((b: any) => b.kind === 'pipeline') || []);
    } catch {}
  }, []);

  useEffect(() => { fetch(); fetchPresets(); }, [fetch, fetchPresets]);

  const handleApplyPreset = async (id: string) => {
    setApplyingPreset(true);
    setPresetNotice(null);
    try {
      await applyPipelinePreset(id);
      setPresetNotice(`Pipeline preset "${id}" applied`);
      const p = await getPipeline();
      setPipeline(p);
      ctx?.refresh();
    } catch (e) {
      setPresetNotice(`Failed: ${e instanceof Error ? e.message : 'Unknown error'}`);
    } finally {
      setApplyingPreset(false);
    }
  };

  const handleTogglePhase = async (phaseId: string, enabled: boolean) => {
    if (!pipeline) return;
    setPipeline({
      ...pipeline,
      config: {
        ...pipeline.config,
        phases: { ...pipeline.config.phases, [phaseId]: { ...pipeline.config.phases[phaseId], enabled } },
      },
    });
  };

  const handleAgentChange = (phaseId: string, agent: string) => {
    if (!pipeline) return;
    setPipeline({
      ...pipeline,
      config: {
        ...pipeline.config,
        phases: { ...pipeline.config.phases, [phaseId]: { ...pipeline.config.phases[phaseId], agent } },
      },
    });
  };

  const handleWhenChange = (phaseId: string, when: string) => {
    if (!pipeline) return;
    setPipeline({
      ...pipeline,
      config: {
        ...pipeline.config,
        phases: { ...pipeline.config.phases, [phaseId]: { ...pipeline.config.phases[phaseId], when } },
      },
    });
  };

  const handleSave = async () => {
    if (!pipeline) return;
    setSaving(true);
    try { await updatePipeline(pipeline.config); } catch (e) { console.error(e); } finally { setSaving(false); }
  };

  const handleReset = async () => {
    try { const p = await resetPipeline(); setPipeline(p); } catch (e) { console.error(e); }
  };

  if (loading) return (
    <div className="flex items-center justify-center h-full"><div className="flex items-center gap-3 text-gray-400"><div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin" />Loading pipeline…</div></div>
  );
  if (!pipeline) return (
    <div className="flex items-center justify-center h-full text-gray-400">No pipeline data.</div>
  );

  const { config } = pipeline;
  const groups = config.groups || [];
  const activePipe = ctx?.config?.active_pipeline;

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-3xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Pipeline</h1>
            <p className="text-sm text-gray-500 mt-1">
              Configure execution phases{activePipe && <span className="ml-2 badge-brand">active: {activePipe}</span>}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={handleReset} className="btn-ghost text-sm gap-2"><RotateCcw size={16} />Reset</button>
            <button onClick={handleSave} disabled={saving} className="btn-primary text-sm gap-2"><Save size={16} />{saving ? 'Saving…' : 'Save'}</button>
          </div>
        </div>

        {/* Preset selector */}
        {presets.length > 0 && (
          <div className="card p-4 space-y-3">
            <div className="flex items-center gap-2 text-sm font-bold"><Layers size={16} className="text-brand-500" />Pipeline Presets</div>
            <div className="flex flex-wrap gap-2">
              {presets.map((p: any) => (
                <button
                  key={p.id}
                  disabled={applyingPreset}
                  onClick={() => handleApplyPreset(p.id)}
                  className={clsx(
                    'px-3 py-1.5 rounded-lg text-xs font-medium transition-all',
                    activePipe === p.id
                      ? 'bg-brand-500 text-white shadow-sm'
                      : 'bg-gray-100 dark:bg-gray-800 text-gray-700 dark:text-gray-300 hover:bg-brand-50 dark:hover:bg-brand-900/20',
                    applyingPreset && 'opacity-50 cursor-not-allowed',
                  )}
                >
                  {p.icon || ''} {p.name}
                  {activePipe === p.id && <span className="ml-1 text-[10px] opacity-70">✓</span>}
                </button>
              ))}
            </div>
            {presetNotice && (
              <div className={clsx('text-xs px-2 py-1 rounded', presetNotice.includes('Failed') ? 'text-red-600 bg-red-50' : 'text-emerald-600 bg-emerald-50')}>
                {presetNotice}
              </div>
            )}
          </div>
        )}

        {/* Execute loop config */}
        <div className="card p-4 grid grid-cols-3 gap-4">
          <div><div className="label">Default Role</div><div className="text-sm font-mono">{config.execute?.default_role || 'worker'}</div></div>
          <div><div className="label">Reviewer</div><div className="text-sm font-mono">{config.execute?.reviewer || 'reviewer'}</div></div>
          <div><div className="label">Max Waves</div><div className="text-sm font-mono">{config.execute?.max_waves || 2}</div></div>
        </div>

        {/* Phase groups */}
        {groups.map((group) => {
          const phases = group.steps.filter((id) => config.phases[id]).map((id) => ({ id, ...config.phases[id] }));
          if (phases.length === 0) return null;
          return (
            <div key={group.id} className="card overflow-hidden">
              <div className={clsx('px-4 py-2 text-xs font-bold', GROUP_COLORS[group.id] || 'bg-gray-50 dark:bg-gray-800')}>{group.label}</div>
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {phases.map((phase) => {
                  const isEnabled = phase.enabled !== false;
                  const defaultAgent = pipeline.defaults?.[phase.id] || '';
                  return (
                    <div key={phase.id} className={clsx('flex items-center gap-4 px-4 py-3 transition-colors', !isEnabled && 'opacity-50')}>
                      <button className="p-1 text-gray-400 hover:text-gray-600 cursor-grab"><GripVertical size={16} /></button>
                      <div className="w-28 shrink-0"><div className="text-sm font-bold">{phase.label || phase.id}</div><div className="text-[10px] text-gray-400 font-mono">{phase.id}</div></div>
                      <div className="flex-1 min-w-0"><div className="text-xs text-gray-500 truncate">{phase.tip}</div></div>
                      <input type="text" value={phase.agent || ''} onChange={(e) => handleAgentChange(phase.id, e.target.value)} placeholder={defaultAgent || 'auto'} className="input w-28 text-xs font-mono py-1.5" disabled={!isEnabled} />
                      <select value={phase.when || 'auto'} onChange={(e) => handleWhenChange(phase.id, e.target.value)} className={clsx('input w-20 text-xs py-1.5', WHEN_COLORS[phase.when || 'auto'])} disabled={!isEnabled}>
                        <option value="always">Always</option><option value="auto">Auto</option><option value="never">Never</option>
                      </select>
                      <button onClick={() => handleTogglePhase(phase.id, !isEnabled)} className={clsx('p-1.5 rounded-lg transition-colors', isEnabled ? 'text-emerald-500 hover:bg-emerald-50' : 'text-gray-400 hover:bg-gray-100')} title={isEnabled ? 'Disable' : 'Enable'}>
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
