import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';

// ── Keyboard-first navigation ──
//
//   ?        open the shortcut sheet
//   g <key>  go to a page (vim/gmail style two-stroke chord)
//   /        focus the run prompt
//   Esc      close whatever is open
//
// Shortcuts never fire while the user is typing into a field or a
// contenteditable region, and never while a modal has focus.

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
  { key: 'k', path: '/blocks', label: 'Blocks' },
  { key: 'f', path: '/files', label: 'Files' },
  { key: 's', path: '/skills', label: 'Skills' },
  { key: 'h', path: '/runs', label: 'Run history' },
  { key: ',', path: '/settings', label: 'Settings' },
];

export const SHORTCUTS: Shortcut[] = [
  { keys: '?', label: 'Show this shortcut sheet', group: 'General' },
  { keys: 'Esc', label: 'Close dialog / shortcut sheet', group: 'General' },
  { keys: '/', label: 'Focus the run prompt', group: 'General' },
  ...GO_TARGETS.map((t) => ({ keys: `g ${t.key}`, label: `Go to ${t.label}`, group: 'Navigation' })),
];

/** True when the event originated in a text-entry context. */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName.toLowerCase();
  if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
  return target.isContentEditable;
}

/** Fired on `/` so the Live view can focus its prompt from anywhere. */
export const FOCUS_PROMPT_EVENT = 'slmcode:focus-prompt';

export function useKeyboardShortcuts() {
  const navigate = useNavigate();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [chord, setChord] = useState<string | null>(null);
  const chordTimer = useRef<number | null>(null);
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;

  useEffect(() => {
    const clearChord = () => {
      if (chordTimer.current !== null) {
        window.clearTimeout(chordTimer.current);
        chordTimer.current = null;
      }
      setChord(null);
    };

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      if (e.key === 'Escape') {
        setSheetOpen(false);
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

  return { sheetOpen, setSheetOpen, chord };
}
