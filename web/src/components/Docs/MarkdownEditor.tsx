import { useState, useEffect, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import { ApiError, getDoc, updateDoc } from '@/api/client';
import { Save, FileText, Loader } from 'lucide-react';
import { useToast } from '@/components/ui/Toast';
import ErrorState from '@/components/ui/ErrorState';

export default function MarkdownEditor() {
  const { docId } = useParams<{ docId: string }>();
  const toast = useToast();
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  const fetchDoc = useCallback(async () => {
    if (!docId) return;
    setLoading(true);
    setLoadError(null);
    try {
      const d = await getDoc(docId);
      setContent(d.content || '');
    } catch (e) {
      // A 404 is a document that does not exist yet: start it empty. Anything
      // else is a real failure the editor must not paper over with a blank page.
      if (e instanceof ApiError && e.status === 404) {
        setContent('');
      } else {
        setLoadError(e);
      }
    } finally {
      setLoading(false);
    }
  }, [docId]);

  useEffect(() => {
    fetchDoc();
  }, [fetchDoc]);

  const handleSave = useCallback(async () => {
    if (!docId) return;
    setSaving(true);
    setSaved(false);
    try {
      await updateDoc(docId, content);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      if (e instanceof ApiError && e.isConflict) {
        toast.push({
          tone: 'warning',
          title: `${docId} not saved`,
          detail: 'A run is active and owns this document — stop it before editing. Your text is kept in the editor.',
        });
      } else {
        toast.reportError(e, `Could not save ${docId}`);
      }
    } finally {
      setSaving(false);
    }
  }, [docId, content, toast]);

  // Keyboard shortcut: Cmd/Ctrl + S
  useEffect(() => {
    const handle = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        handleSave();
      }
    };
    window.addEventListener('keydown', handle);
    return () => window.removeEventListener('keydown', handle);
  }, [handleSave]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400">
        <Loader size={20} className="animate-spin mr-2" />
        Loading…
      </div>
    );
  }

  if (loadError) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <ErrorState error={loadError} what={docId} onRetry={fetchDoc} className="w-full max-w-md" />
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-4 py-2 border-b border-gray-200 dark:border-gray-800 glass shrink-0">
        <div className="flex items-center gap-3">
          <FileText size={18} className="text-gray-400" />
          <div>
            <h2 className="text-sm font-bold">{docId}</h2>
            <p className="text-[10px] text-gray-400">Markdown document editor</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {saved && (
            <span className="text-xs text-emerald-500 animate-fade-in">✓ Saved</span>
          )}
          <button
            onClick={handleSave}
            disabled={saving}
            className="btn-primary text-sm gap-2"
          >
            <Save size={14} />
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>

      {/* Editor */}
      <div className="flex-1 flex overflow-hidden">
        {/* Edit */}
        <div className="flex-1 flex flex-col">
          <div className="px-4 py-1.5 border-b border-gray-100 dark:border-gray-800 text-[10px] font-semibold uppercase text-gray-400 tracking-wider flex items-center gap-2">
            <span>Editor</span>
            <span className="text-gray-300">⌘S to save</span>
          </div>
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            className="flex-1 w-full p-4 font-mono text-sm bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100
                       resize-none focus:outline-none leading-relaxed"
            placeholder={`# ${docId}\n\nStart typing…`}
            spellCheck={false}
          />
        </div>

        {/* Preview */}
        <div className="flex-1 border-l border-gray-200 dark:border-gray-800 hidden lg:flex flex-col">
          <div className="px-4 py-1.5 border-b border-gray-100 dark:border-gray-800 text-[10px] font-semibold uppercase text-gray-400 tracking-wider">
            Preview
          </div>
          <div
            className="flex-1 p-4 overflow-auto prose prose-sm dark:prose-invert max-w-none
                       prose-headings:text-gray-900 dark:prose-headings:text-gray-100
                       prose-code:text-brand-600 dark:prose-code:text-brand-400
                       prose-a:text-brand-500"
            dangerouslySetInnerHTML={{ __html: renderMD(content) }}
          />
        </div>
      </div>

      {/* Status bar */}
      <div className="px-4 py-1 border-t border-gray-200 dark:border-gray-800 glass text-[10px] text-gray-400 flex items-center justify-between shrink-0">
        <span>{docId}</span>
        <span>{content.split('\n').length} lines · {content.length} chars</span>
      </div>
    </div>
  );
}

// Simple markdown renderer (basic, for preview)
function renderMD(text: string): string {
  let html = text
    // Headers
    .replace(/^### (.+)$/gm, '<h3 class="text-base font-semibold mt-4 mb-2">$1</h3>')
    .replace(/^## (.+)$/gm, '<h2 class="text-lg font-bold mt-6 mb-3">$1</h2>')
    .replace(/^# (.+)$/gm, '<h1 class="text-xl font-bold mt-8 mb-4">$1</h1>')
    // Bold / Italic
    .replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>')
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    // Inline code
    .replace(/`([^`]+)`/g, '<code class="bg-gray-100 dark:bg-gray-800 px-1 py-0.5 rounded text-xs font-mono text-brand-600 dark:text-brand-400">$1</code>')
    // Code blocks
    .replace(/```(\w*)\n([\s\S]*?)```/g, '<pre class="bg-gray-100 dark:bg-gray-800 rounded-lg p-3 my-3 overflow-auto text-xs font-mono"><code>$2</code></pre>')
    // Links
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" class="text-brand-500 hover:underline">$1</a>')
    // HR
    .replace(/^---$/gm, '<hr class="border-gray-200 dark:border-gray-700 my-4" />')
    // Lists
    .replace(/^- (.+)$/gm, '<li class="ml-4 list-disc text-sm">$1</li>')
    // Paragraphs
    .replace(/\n\n/g, '</p><p class="text-sm leading-relaxed my-2">');

  html = '<p class="text-sm leading-relaxed my-2">' + html + '</p>';
  return html;
}
