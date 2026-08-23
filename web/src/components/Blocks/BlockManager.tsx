import { useState, useEffect, useCallback, useContext } from 'react';
import type { ReactNode } from 'react';
import { getBlocks, applyPack, applyPipelinePreset, deleteBlock } from '@/api/client';
import { AppContext } from '@/App';
import type { BlockView, BlockCatalogEntry } from '@/types';
import {
  Package,
  Workflow,
  Bot,
  ShieldCheck,
  Play,
  CheckCircle2,
  XCircle,
  ExternalLink,
  Plus,
  Edit3,
  Trash2,
} from 'lucide-react';
import clsx from 'clsx';
import BlockEditor from './BlockEditor';
import { useConfirm } from '@/components/ui/Modal';

const KIND_ICONS: Record<string, ReactNode> = {
  pack: <Package size={20} />,
  pipeline: <Workflow size={20} />,
  agent: <Bot size={20} />,
  quality: <ShieldCheck size={20} />,
};

const KIND_COLORS: Record<string, string> = {
  pack: 'border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20',
  pipeline: 'border-sky-300 dark:border-sky-700 bg-sky-50 dark:bg-sky-900/20',
  agent: 'border-violet-300 dark:border-violet-700 bg-violet-50 dark:bg-violet-900/20',
  quality: 'border-emerald-300 dark:border-emerald-700 bg-emerald-50 dark:bg-emerald-900/20',
};

const KIND_BADGE: Record<string, string> = {
  pack: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
  pipeline: 'bg-sky-100 dark:bg-sky-900/30 text-sky-700 dark:text-sky-300',
  agent: 'bg-violet-100 dark:bg-violet-900/30 text-violet-700 dark:text-violet-300',
  quality: 'bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300',
};

const KIND_TITLES: Record<string, string> = {
  pack: 'Pack',
  pipeline: 'Pipeline',
  agent: 'Agent',
  quality: 'Quality',
};

const TABS = [
  { id: '', label: 'All' },
  { id: 'pack', label: 'Packs' },
  { id: 'pipeline', label: 'Pipelines' },
  { id: 'agent', label: 'Agents' },
  { id: 'quality', label: 'Quality' },
];

interface EditorState {
  kind: string;
  mode: 'create' | 'edit';
  block: BlockCatalogEntry | null;
}

export default function BlockManager() {
  const confirm = useConfirm();
  const ctx = useContext(AppContext);
  const [view, setView] = useState<BlockView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState('');
  const [applying, setApplying] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editor, setEditor] = useState<EditorState | null>(null);

  const fetch = useCallback(async () => {
    try {
      const data = await getBlocks(activeTab || undefined);
      setView(data);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load blocks');
    } finally {
      setLoading(false);
    }
  }, [activeTab]);

  useEffect(() => {
    fetch();
  }, [fetch]);

  const handleApplyPack = async (id: string) => {
    setApplying(id);
    setError(null);
    setNotice(null);
    try {
      const res = await applyPack(id);
      setNotice(`Applied pack "${res.result.pack_id}" — pipeline: ${res.result.pipeline_id || 'none'}`);
      ctx?.refresh();
      fetch();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to apply pack');
    } finally {
      setApplying(null);
    }
  };

  const handleApplyPipeline = async (id: string) => {
    setApplying(id);
    setError(null);
    setNotice(null);
    try {
      const res = await applyPipelinePreset(id);
      setNotice(`Applied pipeline "${res.result.pipeline_id}"`);
      ctx?.refresh();
      fetch();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to apply pipeline');
    } finally {
      setApplying(null);
    }
  };

  // Builtins without a project/user override must not be deleted — deleting
  // requires a custom source or a real file path (override) to remove.
  const canDelete = (block: BlockCatalogEntry) => block.custom || !!block.path;

  const handleDelete = async (block: BlockCatalogEntry) => {
    const title = KIND_TITLES[block.kind] || block.kind;
    const message = block.custom
      ? `Delete ${title.toLowerCase()} "${block.name || block.id}"?`
      : `Delete ${title.toLowerCase()} "${block.name || block.id}"? This removes the override of the builtin block.`;
    const ok = await confirm({
      title: `Delete ${title.toLowerCase()} "${block.name || block.id}"?`,
      description: block.custom ? undefined : 'This removes the override of the builtin block.',
      confirmLabel: 'Delete',
    });
    if (!ok) return;
    setError(null);
    setNotice(null);
    try {
      await deleteBlock(block.kind, block.id);
      setNotice(`Deleted ${title.toLowerCase()} "${block.id}"`);
      fetch();
      ctx?.refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete block');
    }
  };

  const handleSaved = (kind: string, id: string, created: boolean) => {
    setEditor(null);
    setNotice(`${KIND_TITLES[kind] || kind} "${id}" ${created ? 'created' : 'updated'}`);
    fetch();
    ctx?.refresh();
  };

  const getBlocksForTab = (): BlockCatalogEntry[] => {
    if (!view) return [];
    if (activeTab === '') return view.blocks;
    return view.blocks.filter((b) => b.kind === activeTab);
  };

  const blocks = getBlocksForTab();

  return (
    <div className="max-w-5xl mx-auto p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Building Blocks</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
            Marketplace-ready YAML presets: language packs, pipelines, agents, and quality checks
          </p>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {activeTab && (
            <button
              onClick={() => setEditor({ kind: activeTab, mode: 'create', block: null })}
              className="btn-primary gap-1.5 text-sm"
            >
              <Plus size={15} />
              New {KIND_TITLES[activeTab]}
            </button>
          )}
          {view?.active_pack && (
            <div className="flex items-center gap-2 px-3 py-1.5 rounded-full bg-brand-50 dark:bg-brand-900/20 text-brand-700 dark:text-brand-300 text-sm font-medium">
              <CheckCircle2 size={16} />
              Active: {view.active_pack}
            </div>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 p-1 bg-gray-100 dark:bg-gray-800 rounded-lg">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            onClick={() => { setActiveTab(tab.id); setLoading(true); }}
            className={clsx(
              'flex-1 px-4 py-2 text-sm font-medium rounded-md transition-all duration-150',
              activeTab === tab.id
                ? 'bg-white dark:bg-gray-700 text-gray-900 dark:text-white shadow-sm'
                : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200',
            )}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* Notice */}
      {notice && (
        <div className="flex items-center gap-2 px-4 py-3 rounded-lg bg-emerald-50 dark:bg-emerald-900/20 text-emerald-700 dark:text-emerald-300 text-sm">
          <CheckCircle2 size={16} />
          {notice}
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="flex items-center gap-2 px-4 py-3 rounded-lg bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300 text-sm">
          <XCircle size={16} />
          {error}
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-500" />
        </div>
      )}

      {/* Blocks grid */}
      {!loading && blocks.length === 0 && (
        <div className="text-center py-20 text-gray-400 dark:text-gray-500">
          <Package size={48} className="mx-auto mb-4 opacity-50" />
          <p className="text-lg font-medium">No blocks found</p>
          <p className="text-sm mt-1">
            {activeTab
              ? `Add YAML blocks to .slmcode/blocks/ or click "New ${KIND_TITLES[activeTab]}" above`
              : 'Add YAML blocks to .slmcode/blocks/ or ~/.slmcode/blocks/'}
          </p>
        </div>
      )}

      {!loading && blocks.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {blocks.map((block) => (
            <div
              key={`${block.kind}-${block.id}`}
              className={clsx(
                'group relative rounded-xl border p-5 transition-all duration-150 hover:shadow-md',
                KIND_COLORS[block.kind] || 'border-gray-200 dark:border-gray-700',
              )}
            >
              {/* Hover actions */}
              <div className="absolute right-3 top-3 flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
                <button
                  onClick={() => setEditor({ kind: block.kind, mode: 'edit', block })}
                  className="btn-ghost rounded-lg p-1.5"
                  title="Edit"
                >
                  <Edit3 size={14} />
                </button>
                {canDelete(block) ? (
                  <button
                    onClick={() => handleDelete(block)}
                    className="btn-ghost rounded-lg p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                    title="Delete"
                  >
                    <Trash2 size={14} />
                  </button>
                ) : (
                  <span className="relative inline-flex group/trash">
                    <button
                      disabled
                      className="btn-ghost cursor-not-allowed rounded-lg p-1.5 text-gray-300 dark:text-gray-600"
                      title="Builtin — edit to create override"
                    >
                      <Trash2 size={14} />
                    </button>
                    <span className="pointer-events-none absolute right-0 top-full z-50 mt-1 whitespace-nowrap rounded-md bg-gray-900 px-2.5 py-1 text-[10px] text-white opacity-0 shadow-lg transition-opacity group-hover/trash:opacity-100 dark:bg-gray-100 dark:text-gray-900">
                      builtin — edit to create override
                    </span>
                  </span>
                )}
              </div>

              <div className="flex items-start gap-3">
                <div className="mt-0.5 text-gray-400 dark:text-gray-500">
                  {KIND_ICONS[block.kind] || <Package size={20} />}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <span
                      className={clsx(
                        'px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase',
                        KIND_BADGE[block.kind] || 'bg-gray-100 dark:bg-gray-800',
                      )}
                    >
                      {block.kind}
                    </span>
                    {block.builtin && (
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-medium bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400">
                        builtin
                      </span>
                    )}
                    {block.custom && (
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-medium bg-brand-100 dark:bg-brand-900/30 text-brand-700 dark:text-brand-300">
                        custom
                      </span>
                    )}
                  </div>
                  <h3 className="font-semibold text-gray-900 dark:text-white truncate">
                    {block.icon ? `${block.icon} ` : ''}{block.name}
                  </h3>
                  <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">
                    {block.id}
                    {block.version && ` · v${block.version}`}
                    {block.language && ` · ${block.language}`}
                  </p>
                  {block.description && (
                    <p className="text-sm text-gray-600 dark:text-gray-300 mt-2 line-clamp-2">
                      {block.description}
                    </p>
                  )}
                  {block.tags && block.tags.length > 0 && (
                    <div className="flex flex-wrap gap-1 mt-2">
                      {block.tags.slice(0, 4).map((tag) => (
                        <span
                          key={tag}
                          className="px-1.5 py-0.5 rounded text-[10px] bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400"
                        >
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}

                  {/* Actions */}
                  <div className="flex gap-2 mt-4">
                    {block.kind === 'pack' && (
                      <button
                        onClick={() => handleApplyPack(block.id)}
                        disabled={applying === block.id}
                        className={clsx(
                          'flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-colors',
                          'bg-brand-500 hover:bg-brand-600 text-white',
                          applying === block.id && 'opacity-50 cursor-not-allowed',
                        )}
                      >
                        {applying === block.id ? (
                          <div className="animate-spin rounded-full h-3 w-3 border-b-2 border-white" />
                        ) : (
                          <Play size={12} />
                        )}
                        Apply Pack
                      </button>
                    )}
                    {block.kind === 'pipeline' && (
                      <button
                        onClick={() => handleApplyPipeline(block.id)}
                        disabled={applying === block.id}
                        className={clsx(
                          'flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-colors',
                          'bg-sky-500 hover:bg-sky-600 text-white',
                          applying === block.id && 'opacity-50 cursor-not-allowed',
                        )}
                      >
                        {applying === block.id ? (
                          <div className="animate-spin rounded-full h-3 w-3 border-b-2 border-white" />
                        ) : (
                          <Play size={12} />
                        )}
                        Apply Pipeline
                      </button>
                    )}
                    {block.path && (
                      <span className="flex items-center gap-1 text-[10px] text-gray-400 dark:text-gray-500" title={block.path}>
                        <ExternalLink size={10} />
                        {block.source || 'file'}
                      </span>
                    )}
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create/Edit modal */}
      {editor && (
        <BlockEditor
          open
          kind={editor.kind}
          mode={editor.mode}
          block={editor.block}
          onClose={() => setEditor(null)}
          onSaved={handleSaved}
        />
      )}
    </div>
  );
}
