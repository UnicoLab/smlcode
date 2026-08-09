import { useEffect, useState } from 'react';
import { getBlocks, applyPack } from '@/api/client';
import clsx from 'clsx';

interface PackSelectorProps {
  currentPack: string;
  currentPipeline: string;
  onApplied: () => void;
}

const PACK_ICONS: Record<string, string> = {
  go: '🐹',
  python: '🐍',
  react: '⚛️',
};

export default function PackSelector({ currentPack, currentPipeline, onApplied }: PackSelectorProps) {
  const [packs, setPacks] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await getBlocks('pack');
        if (!cancelled) setPacks(data.blocks?.filter((b: any) => b.kind === 'pack') || []);
      } catch {} finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [currentPack]);

  const isActive = (pack: any) =>
    (currentPack && currentPack === pack.id) ||
    (currentPipeline && currentPipeline === pack.id);

  const handleApply = async (pack: any) => {
    setApplying(pack.id);
    setNotice(null);
    try {
      const res = await applyPack(pack.id, { materialize_agents: true });
      setNotice(`Applied ${pack.name} → pipeline:${res.result?.pipeline_id || 'none'} qa:${res.result?.qa_gate_command || 'auto'}`);
      onApplied();
      const refreshed = await getBlocks('pack');
      setPacks(refreshed.blocks?.filter((b: any) => b.kind === 'pack') || []);
    } catch (e) {
      setNotice(`Failed: ${e instanceof Error ? e.message : 'Unknown error'}`);
    } finally {
      setApplying(null);
    }
  };

  if (loading) {
    return <div className="text-sm text-gray-400 py-4 text-center">Loading packs…</div>;
  }

  if (packs.length === 0) {
    return <div className="text-sm text-gray-400 py-4 text-center">No packs available. Try adding blocks to .slmcode/blocks/packs/</div>;
  }

  return (
    <div className="space-y-4">
      {notice && (
        <div className={clsx(
          'text-xs px-3 py-2 rounded-lg',
          notice.startsWith('Failed')
            ? 'text-red-600 bg-red-50 dark:bg-red-900/20'
            : 'text-emerald-600 bg-emerald-50 dark:bg-emerald-900/20'
        )}>
          {notice}
        </div>
      )}
      <div className="grid grid-cols-3 gap-3">
        {packs.map((pack) => {
          const active = isActive(pack);
          const busy = applying === pack.id;
          return (
            <button
              key={pack.id}
              disabled={!!applying}
              onClick={() => handleApply(pack)}
              className={clsx(
                'relative flex flex-col items-center gap-3 p-4 rounded-xl border-2 transition-all duration-200',
                'hover:shadow-lg hover:-translate-y-0.5 disabled:opacity-60',
                busy && 'scale-95',
                active
                  ? 'border-amber-500 bg-amber-50 dark:bg-amber-900/20 shadow-md'
                  : 'border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 hover:border-amber-300 dark:hover:border-amber-700',
              )}
            >
              {active && (
                <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white dark:ring-gray-900 animate-pulse" />
              )}
              <div className="text-3xl">{PACK_ICONS[pack.id] || pack.icon || '📦'}</div>
              <div className="text-center">
                <div className={clsx(
                  'text-sm font-bold',
                  active ? 'text-amber-700 dark:text-amber-300' : 'text-gray-700 dark:text-gray-300',
                )}>
                  {pack.name}
                </div>
                <div className="text-[10px] text-gray-400 mt-0.5">
                  {pack.language || 'multi'} · v{pack.version || '1.0.0'}
                </div>
              </div>
              <div className="flex items-center gap-1">
                <span className={clsx('badge text-[9px]', active ? 'badge-brand' : 'badge-neutral')}>
                  {busy ? 'Applying…' : active ? 'Active' : 'Apply'}
                </span>
              </div>
            </button>
          );
        })}
      </div>
      <p className="text-[11px] text-gray-400">
        Language packs set pipeline, quality checks, and language-specific agents. Apply to switch the entire workflow.
      </p>
    </div>
  );
}
