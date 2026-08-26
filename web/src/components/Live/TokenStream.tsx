import { memo } from 'react';
import { Sparkles } from 'lucide-react';
import { useStickToBottom } from '@/hooks/useUiState';

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

function TokenStream({ text, running }: Props) {
  // Pin to the bottom by writing this element's own scrollTop. The previous
  // `endRef.current.scrollIntoView()` scrolled every ancestor scroll container
  // too — including the page's <main> — so each token nudged the whole page.
  const bodyRef = useStickToBottom<HTMLDivElement>(text);

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
        ref={bodyRef}
        // Height scales with the viewport instead of a fixed 14rem: on a short
        // laptop screen that box was most of what was left for the log, and on
        // a tall monitor it wasted the room it could have used.
        className="max-h-[clamp(6rem,18vh,20rem)] overflow-y-auto px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-words"
        aria-live="polite"
        aria-atomic="false"
      >
        {text}
        <span
          className={running ? 'ml-0.5 inline-block h-3 w-1.5 animate-pulse bg-brand-500 align-middle' : 'hidden'}
          aria-hidden="true"
        />
      </div>
    </section>
  );
}

// Memoised: the parent re-renders on every stream flush, and re-rendering a
// 20,000-character <pre> that did not change is pure cost.
export default memo(TokenStream);
