import { useEffect, useRef } from 'react';
import { Sparkles } from 'lucide-react';

// ── Live token stream (capability B) ──
//
// Renders `stream.KindToken` deltas as they arrive. The engine may not emit
// them yet: `useLiveStream` accumulates any of `token` / `delta` /
// `token_delta` and this component renders nothing at all when the buffer is
// empty, so today it is simply invisible and costs nothing.

interface Props {
  text: string;
  running: boolean;
}

export default function TokenStream({ text, running }: Props) {
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' });
  }, [text]);

  if (!text) return null;

  return (
    <section
      aria-label="Live model output"
      className="rounded-lg border border-brand-500/30 bg-brand-50/40 dark:bg-brand-950/20"
    >
      <h3 className="flex items-center gap-1.5 border-b border-brand-500/20 px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-brand-700 dark:text-brand-300">
        <Sparkles size={11} aria-hidden="true" />
        Streaming output
        {running && <span className="ml-1 h-1.5 w-1.5 animate-pulse rounded-full bg-brand-500" aria-hidden="true" />}
      </h3>
      <div
        className="max-h-56 overflow-y-auto px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words"
        aria-live="polite"
        aria-atomic="false"
      >
        {text}
        <span
          className={running ? 'ml-0.5 inline-block h-3 w-1.5 animate-pulse bg-brand-500 align-middle' : 'hidden'}
          aria-hidden="true"
        />
        <div ref={endRef} />
      </div>
    </section>
  );
}
