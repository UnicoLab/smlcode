import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  COMMAND_PALETTE_EVENT,
  FOCUS_PROMPT_EVENT,
  RUN_PROMPT_EVENT,
  STOP_RUN_EVENT,
  emit,
} from '@/components/ui/events';

// ── Keyboard-first navigation ──
//
//   ?          open the shortcut sheet
//   g <key>    go to a page (vim/gmail style two-stroke chord)
//   /          focus the run prompt
//   ⌘K         command palette
//   ⌘↵ / ⌘.    run / stop, while the run prompt is focused
//   Esc        close whatever is open
//
// Plain-key shortcuts never fire while the user is typing into a field or a
// contenteditable region, and never while a modal has focus. The modifier
// shortcuts are the exception: ⌘↵ and ⌘. exist precisely for the prompt box.
//
// The run and stop shortcuts do not know how to start a run: they dispatch
// RUN_PROMPT_EVENT / STOP_RUN_EVENT on `window` and the Live view, which owns
// the prompt and the run request, listens (see components/ui/events.ts).

export interface Shortcut {
  keys: string;
  label: string;
  group: string;
}

/** GO_TARGETS is both the routing table and the documentation. */
export const GO_TARGETS: Array<{ key: string; path: string; label: string }> = [
  { key: 'l', path: '/', label: 'Live' },
  { key: 'b', path: '/board', label: 'Board' },
  { key: 'r', path: '/review', label: 'Review queue' },
  { key: 'p', path: '/pipeline', label: 'Pipeline' },
  { key: 'a', path: '/agents', label: 'Agents' },
  { key: 't', path: '/teams', label: 'Teams' },
  { key: 'k', path: '/blocks', label: 'Blocks' },
  { key: 'f', path: '/files', label: 'Files' },
  { key: 's', path: '/skills', label: 'Skills' },
  { key: 'h', path: '/runs', label: 'Run history' },
  { key: ',', path: '/settings', label: 'Settings' },
];

/** Rendered as ⌘ on Apple platforms and Ctrl elsewhere. */
export const MOD_LABEL =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl';

export const SHORTCUTS: Shortcut[] = [
  { keys: '?', label: 'Show this shortcut sheet', group: 'General' },
  { keys: `${MOD_LABEL} K`, label: 'Command palette — pages, tasks, agents, teams, runs, theme', group: 'General' },
  { keys: 'Esc', label: 'Close dialog / shortcut sheet', group: 'General' },
  { keys: '/', label: 'Focus the run prompt', group: 'Run' },
  { keys: `${MOD_LABEL} ↵`, label: 'Run the prompt (while it is focused)', group: 'Run' },
  { keys: `${MOD_LABEL} .`, label: 'Stop the active run (while the prompt is focused)', group: 'Run' },
  ...GO_TARGETS.map((t) => ({ keys: `g ${t.key}`, label: `Go to ${t.label}`, group: 'Navigation' })),
];

/** True when the event originated in a text-entry context. */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName.toLowerCase();
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
  return target.isContentEditable;
}

/**
 * The run prompt is recognised by its accessible name ("Run prompt") or an
 * explicit data-run-prompt attribute, so the shortcut needs no ref plumbing.
 */
export function isRunPrompt(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.getAttribute('aria-label') === 'Run prompt' || target.hasAttribute('data-run-prompt');
}

// Re-exported so existing imports keep working; the name now lives with the
// other window events.
export { FOCUS_PROMPT_EVENT, RUN_PROMPT_EVENT, STOP_RUN_EVENT };

export function useKeyboardShortcuts() {
  const navigate = useNavigate();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [chord, setChord] = useState<string | null>(null);
  const chordTimer = useRef<number | null>(null);
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;

  // Anything can ask for the palette (a button, another shortcut).
  useEffect(() => {
    const toggle = () => setPaletteOpen((v) => !v);
    window.addEventListener(COMMAND_PALETTE_EVENT, toggle);
    return () => window.removeEventListener(COMMAND_PALETTE_EVENT, toggle);
  }, []);

  useEffect(() => {
    const clearChord = () => {
      if (chordTimer.current !== null) {
        window.clearTimeout(chordTimer.current);
        chordTimer.current = null;
      }
      setChord(null);
    };

    const onKeyDown = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;

      if (mod && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((v) => !v);
        return;
      }
      if (mod && isRunPrompt(e.target)) {
        if (e.key === 'Enter') {
          e.preventDefault();
          emit(RUN_PROMPT_EVENT);
          return;
        }
        if (e.key === '.') {
          e.preventDefault();
          emit(STOP_RUN_EVENT);
          return;
        }
      }
      if (mod || e.altKey) return;

      if (e.key === 'Escape') {
        setSheetOpen(false);
        setPaletteOpen(false);
        clearChord();
        return;
      }
      if (isTypingTarget(e.target)) return;

      // Second stroke of a `g` chord.
      if (chord === 'g') {
        clearChord();
        const target = GO_TARGETS.find((t) => t.key === e.key.toLowerCase());
        if (target) {
          e.preventDefault();
          navigateRef.current(target.path);
        }
        return;
      }

      if (e.key === 'g') {
        e.preventDefault();
        setChord('g');
        chordTimer.current = window.setTimeout(clearChord, 1500);
        return;
      }
      if (e.key === '?') {
        e.preventDefault();
        setSheetOpen((v) => !v);
        return;
      }
      if (e.key === '/') {
        e.preventDefault();
        window.dispatchEvent(new CustomEvent(FOCUS_PROMPT_EVENT));
      }
    };

    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      if (chordTimer.current !== null) window.clearTimeout(chordTimer.current);
    };
  }, [chord]);

  return { sheetOpen, setSheetOpen, paletteOpen, setPaletteOpen, chord };
}
