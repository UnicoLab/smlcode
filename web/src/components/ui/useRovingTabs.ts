import { useCallback, useId } from 'react';
import type { KeyboardEvent } from 'react';

// ── Roving tabindex for a tablist ──
//
// The WAI-ARIA tabs pattern: one Tab stop for the whole strip (the active
// tab), arrows move between tabs, Home/End jump to the ends, and each tab
// names the panel it controls. Every tab strip in Studio hand-rolled a row
// of buttons instead, which put eight tab stops between the header and the
// content on every page.

export interface RovingTab<ID extends string> {
  id: ID;
}

export function useRovingTabs<ID extends string>(
  tabs: ReadonlyArray<RovingTab<ID>>,
  active: ID,
  onChange: (id: ID) => void,
) {
  const base = useId();
  const tabId = useCallback((id: ID) => `${base}-tab-${id}`, [base]);
  const panelId = useCallback((id: ID) => `${base}-panel-${id}`, [base]);

  const onKeyDown = useCallback(
    (e: KeyboardEvent<HTMLElement>) => {
      const idx = tabs.findIndex((t) => t.id === active);
      if (idx < 0) return;
      let next = idx;
      switch (e.key) {
        case 'ArrowRight':
        case 'ArrowDown':
          next = (idx + 1) % tabs.length;
          break;
        case 'ArrowLeft':
        case 'ArrowUp':
          next = (idx - 1 + tabs.length) % tabs.length;
          break;
        case 'Home':
          next = 0;
          break;
        case 'End':
          next = tabs.length - 1;
          break;
        default:
          return;
      }
      e.preventDefault();
      const id = tabs[next].id;
      onChange(id);
      document.getElementById(tabId(id))?.focus();
    },
    [tabs, active, onChange, tabId],
  );

  /** Props for one tab button. */
  const tabProps = useCallback(
    (id: ID) => ({
      id: tabId(id),
      role: 'tab' as const,
      type: 'button' as const,
      'aria-selected': id === active,
      'aria-controls': panelId(id),
      tabIndex: id === active ? 0 : -1,
      onClick: () => onChange(id),
      onKeyDown,
    }),
    [active, onChange, onKeyDown, tabId, panelId],
  );

  /** Props for the panel the active tab controls. */
  const panelProps = useCallback(
    (id: ID) => ({
      id: panelId(id),
      role: 'tabpanel' as const,
      'aria-labelledby': tabId(id),
      tabIndex: 0,
    }),
    [panelId, tabId],
  );

  return { tabProps, panelProps, listProps: { role: 'tablist' as const } };
}
