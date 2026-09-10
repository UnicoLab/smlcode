import { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Archive, Bot, Compass, ListChecks, Moon, Search, Sun, Users } from 'lucide-react';
import type { ReactNode } from 'react';
import clsx from 'clsx';
import { AppContext } from '@/App';
import { getAgents, getQueries, getTasks, getTeams } from '@/api/client';
import { GO_TARGETS } from '@/hooks/useKeyboard';
import { useFocusTrap } from './Modal';

// ── ⌘K ──
//
// One box that reaches everything with a name: pages, tasks by id or title,
// agents, teams, recent runs, and the theme. Self-contained: it loads its
// catalogue when it opens (four best-effort requests) and navigates with the
// router; nothing else in the app has to know it exists.

interface Command {
  id: string;
  group: 'Pages' | 'Tasks' | 'Agents' | 'Teams' | 'Recent runs' | 'Theme';
  label: string;
  hint?: string;
  keywords: string;
  icon: ReactNode;
  run: () => void;
}

interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
}

const MAX_RESULTS = 24;

export default function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const ctx = useContext(AppContext);
  const navigate = useNavigate();
  const panelRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [loaded, setLoaded] = useState<Command[]>([]);

  useFocusTrap(panelRef, { active: open, onEscape: onClose, initialFocusRef: inputRef });

  // Reset and load on open. Each request fails on its own; a page with no
  // board still gets pages, agents and the theme.
  useEffect(() => {
    if (!open) return undefined;
    setQuery('');
    setActive(0);
    let alive = true;
    const go = (path: string) => () => {
      onClose();
      navigate(path);
    };
    Promise.allSettled([getTasks(), getAgents(), getTeams(), getQueries()]).then(([tasks, agents, teams, runs]) => {
      if (!alive) return;
      const out: Command[] = [];
      if (tasks.status === 'fulfilled') {
        for (const t of tasks.value?.tasks ?? []) {
          out.push({
            id: `task:${t.id}`,
            group: 'Tasks',
            label: `${t.id} · ${t.title}`,
            hint: [t.column, t.squad].filter(Boolean).join(' · '),
            keywords: `${t.id} ${t.title} ${t.role} ${t.squad ?? ''} ${t.column}`,
            icon: <ListChecks size={14} aria-hidden="true" />,
            run: go(`/?task=${encodeURIComponent(t.id)}`),
          });
        }
      }
      if (agents.status === 'fulfilled') {
        for (const a of agents.value ?? []) {
          out.push({
            id: `agent:${a.id}`,
            group: 'Agents',
            label: a.title || a.id,
            hint: a.id,
            keywords: `${a.id} ${a.title ?? ''} ${a.description ?? ''}`,
            icon: <Bot size={14} aria-hidden="true" />,
            run: go(`/agents?agent=${encodeURIComponent(a.id)}`),
          });
        }
      }
      if (teams.status === 'fulfilled') {
        for (const t of teams.value?.teams ?? []) {
          out.push({
            id: `team:${t.id}`,
            group: 'Teams',
            label: t.name || t.id,
            hint: t.id,
            keywords: `${t.id} ${t.name ?? ''} ${t.charter ?? ''}`,
            icon: <Users size={14} aria-hidden="true" />,
            run: go(`/teams?team=${encodeURIComponent(t.id)}`),
          });
        }
      }
      if (runs.status === 'fulfilled') {
        for (const r of (runs.value ?? []).slice(0, 15)) {
          out.push({
            id: `run:${r.id}`,
            group: 'Recent runs',
            label: r.query || r.id,
            hint: r.id,
            keywords: `${r.id} ${r.query} ${r.summary}`,
            icon: <Archive size={14} aria-hidden="true" />,
            run: go(`/runs?run=${encodeURIComponent(r.id)}`),
          });
        }
      }
      setLoaded(out);
    });
    return () => {
      alive = false;
    };
  }, [open, navigate, onClose]);

  const commands = useMemo<Command[]>(() => {
    const go = (path: string) => () => {
      onClose();
      navigate(path);
    };
    const pages: Command[] = GO_TARGETS.map((t) => ({
      id: `page:${t.path}`,
      group: 'Pages',
      label: t.label,
      hint: `g ${t.key}`,
      keywords: `${t.label} ${t.path} go page`,
      icon: <Compass size={14} aria-hidden="true" />,
      run: go(t.path),
    }));
    const theme: Command = {
      id: 'theme:toggle',
      group: 'Theme',
      label: ctx?.dark ? 'Switch to light theme' : 'Switch to dark theme',
      keywords: 'theme dark light mode appearance toggle',
      icon: ctx?.dark ? <Sun size={14} aria-hidden="true" /> : <Moon size={14} aria-hidden="true" />,
      run: () => {
        ctx?.toggleDark();
        onClose();
      },
    };
    return [...pages, theme, ...loaded];
  }, [loaded, ctx, navigate, onClose]);

  const results = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return commands.filter((c) => c.group === 'Pages' || c.group === 'Theme').slice(0, MAX_RESULTS);
    const terms = needle.split(/\s+/);
    return commands
      .map((c) => {
        const hay = `${c.label} ${c.keywords}`.toLowerCase();
        if (!terms.every((t) => hay.includes(t))) return null;
        const first = c.label.toLowerCase().indexOf(terms[0]);
        return { c, score: first < 0 ? 100 : first };
      })
      .filter((x): x is { c: Command; score: number } => x !== null)
      .sort((a, b) => a.score - b.score)
      .slice(0, MAX_RESULTS)
      .map((x) => x.c);
  }, [commands, query]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    document.getElementById(`palette-item-${active}`)?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  if (!open) return null;

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      results[active]?.run();
    }
  };

  let lastGroup = '';
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center px-4 pt-[12vh]">
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" onClick={onClose} aria-hidden="true" />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="relative z-10 w-full max-w-xl overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-gray-800 dark:bg-gray-900"
      >
        <div className="flex items-center gap-2 border-b border-gray-100 px-3 dark:border-gray-800">
          <Search size={15} className="shrink-0 text-gray-400" aria-hidden="true" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-listbox"
            aria-activedescendant={results.length > 0 ? `palette-item-${active}` : undefined}
            aria-autocomplete="list"
            placeholder="Go to a page, a task, an agent, a team, a run…"
            className="h-11 w-full bg-transparent text-sm outline-none placeholder:text-gray-400"
          />
          <kbd className="hidden rounded border border-gray-200 px-1.5 py-0.5 font-mono text-[10px] text-gray-400 sm:inline dark:border-gray-700">
            esc
          </kbd>
        </div>
        <ul id="palette-listbox" role="listbox" aria-label="Commands" className="max-h-[50vh] overflow-y-auto py-1">
          {results.length === 0 && (
            <li className="px-4 py-6 text-center text-xs text-gray-400">Nothing matches “{query}”.</li>
          )}
          {results.map((c, i) => {
            const header = c.group !== lastGroup ? c.group : null;
            lastGroup = c.group;
            return (
              <li key={c.id} role="presentation">
                {header && (
                  <div className="px-3 pb-0.5 pt-2 text-[10px] font-semibold uppercase tracking-wider text-gray-400">{header}</div>
                )}
                <div
                  id={`palette-item-${i}`}
                  role="option"
                  aria-selected={i === active}
                  tabIndex={-1}
                  onMouseEnter={() => setActive(i)}
                  onClick={c.run}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      c.run();
                    }
                  }}
                  className={clsx(
                    'mx-1 flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm',
                    i === active ? 'bg-brand-50 text-brand-800 dark:bg-brand-900/30 dark:text-brand-100' : 'text-gray-700 dark:text-gray-200',
                  )}
                >
                  <span className="shrink-0 text-gray-400">{c.icon}</span>
                  <span className="min-w-0 flex-1 truncate">{c.label}</span>
                  {c.hint && <span className="shrink-0 font-mono text-[10px] text-gray-400">{c.hint}</span>}
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
