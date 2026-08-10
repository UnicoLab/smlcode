import { useEffect, useState } from 'react';
import { Download, X } from 'lucide-react';
import { getUpdateInfo } from '@/api/client';
import type { UpdateInfo } from '@/types';

const DISMISS_KEY = 'slmcode:update-banner-dismissed';

export default function UpdateBanner() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [dismissed, setDismissed] = useState<boolean>(() => {
    try {
      return sessionStorage.getItem(DISMISS_KEY) === '1';
    } catch {
      return false;
    }
  });

  useEffect(() => {
    let cancelled = false;
    getUpdateInfo()
      .then((res) => {
        if (!cancelled) setInfo(res);
      })
      .catch(() => {
        // Endpoint may not exist yet (dev servers / older backend) — stay silent.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (
    dismissed ||
    !info ||
    !info.update_available ||
    !info.latest ||
    info.latest === info.current
  ) {
    return null;
  }

  const dismiss = () => {
    setDismissed(true);
    try {
      sessionStorage.setItem(DISMISS_KEY, '1');
    } catch {
      /* ignore */
    }
  };

  return (
    <div className="bg-amber-50 dark:bg-amber-950/40 border-b border-amber-200 dark:border-amber-800 text-amber-800 dark:text-amber-200 text-xs px-4 py-2 flex items-center gap-2">
      <Download size={14} className="shrink-0" />
      <span>
        New version <strong>v{info.latest}</strong> available (you have v{info.current}) — update now
      </span>
      {info.release_url && (
        <a
          href={info.release_url}
          target="_blank"
          rel="noreferrer"
          className="shrink-0 inline-flex items-center px-2 py-0.5 rounded border border-amber-300 dark:border-amber-700 bg-amber-100 dark:bg-amber-900/50 hover:bg-amber-200 dark:hover:bg-amber-800 transition-colors font-medium"
        >
          View Release
        </a>
      )}
      <button
        onClick={dismiss}
        className="ml-auto shrink-0 p-0.5 rounded hover:bg-amber-200/60 dark:hover:bg-amber-800/60 transition-colors"
        title="Dismiss"
        aria-label="Dismiss update banner"
      >
        <X size={14} />
      </button>
    </div>
  );
}
