import { useContext, useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutDashboard,
  Kanban,
  Workflow,
  Bot,
  Puzzle,
  FileText,
  Archive,
  Activity,
  Cpu,
  Wifi,
  WifiOff,
  Package,
  ChevronLeft,
  ChevronRight,
  FileCode,
  FileDiff,
} from 'lucide-react';
import { AppContext } from '@/App';
import { useMediaQuery } from '@/hooks/useUiState';
import { getHealth } from '@/api/client';
import type { Health } from '@/types';
import clsx from 'clsx';

interface NavItem {
  to: string;
  label: string;
  icon: React.ReactNode;
  /** Health field whose count is shown as a badge. */
  badge?: 'pending';
}

const navItems: NavItem[] = [
  { to: '/', label: 'Live', icon: <LayoutDashboard size={18} /> },
  { to: '/board', label: 'Board', icon: <Kanban size={18} /> },
  { to: '/review', label: 'Review', icon: <FileDiff size={18} />, badge: 'pending' },
  { to: '/pipeline', label: 'Pipeline', icon: <Workflow size={18} /> },
  { to: '/agents', label: 'Agents', icon: <Bot size={18} /> },
  { to: '/blocks', label: 'Blocks', icon: <Package size={18} /> },
  { to: '/skills', label: 'Skills', icon: <Puzzle size={18} /> },
  { to: '/files', label: 'Files', icon: <FileCode size={18} /> },
  { to: '/runs', label: 'Runs', icon: <Archive size={18} /> },
];

const docItems: NavItem[] = [
  { to: '/docs/CONTEXT.md', label: 'CONTEXT.md', icon: <FileText size={16} /> },
  { to: '/docs/PLAN.md', label: 'PLAN.md', icon: <FileText size={16} /> },
  { to: '/docs/TASKS.md', label: 'TASKS.md', icon: <FileText size={16} /> },
  { to: '/docs/SCRATCH.md', label: 'SCRATCH.md', icon: <FileText size={16} /> },
];

export default function Sidebar() {
  const ctx = useContext(AppContext);
  const [liveHealth, setLiveHealth] = useState<Health | null>(null);

  const isDesktop = useMediaQuery('(min-width: 1024px)');

  const [isCollapsed, setIsCollapsed] = useState(() => {
    try {
      return localStorage.getItem('slmcode-sidebar-collapsed') === 'true';
    } catch {
      return false;
    }
  });

  // Below `lg` the rail is always icons. At 224px wide the expanded nav took
  // 60% of a 375px screen, leaving the page it navigates to to fight for the
  // rest — so the breakpoint forces the compact form and hides the toggle that
  // would undo it. The user's own preference is preserved, not overwritten: it
  // applies again the moment there is room for it.
  const compact = isCollapsed || !isDesktop;

  const toggleCollapsed = () => {
    setIsCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem('slmcode-sidebar-collapsed', String(next));
      } catch {
        /* private mode — collapse state simply does not persist */
      }
      return next;
    });
  };

  // The connection truth lives in App (EventSource state + a 10s health poll);
  // this only needs the run flag and the pending-review count, and only while
  // the API is actually reachable.
  useEffect(() => {
    if (ctx?.connection === 'down') {
      setLiveHealth(null);
      return undefined;
    }
    let cancelled = false;
    const tick = async () => {
      try {
        const h = await getHealth();
        if (!cancelled) setLiveHealth(h);
      } catch {
        if (!cancelled) setLiveHealth(null);
      }
    };
    tick();
    const interval = setInterval(tick, 15000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [ctx?.connection]);

  const pendingCount = liveHealth?.pending ?? 0;
  const online = ctx ? ctx.connection === 'live' : Boolean(liveHealth?.ok);

  const linkClass = ({ isActive }: { isActive: boolean }) =>
    clsx(
      'flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-all duration-150',
      isActive
        ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-700 dark:text-brand-300'
        : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 hover:text-gray-900 dark:hover:text-gray-100',
      compact && 'justify-center px-0 w-10 h-10 mx-auto',
    );

  const docLinkClass = ({ isActive }: { isActive: boolean }) =>
    clsx(
      'flex items-center gap-2 px-3 py-1.5 rounded-md text-xs transition-colors',
      isActive
        ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-600 dark:text-brand-400'
        : 'text-gray-500 dark:text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800',
      compact && 'justify-center px-0 w-8 h-8 mx-auto',
    );

  return (
    <div
      className={clsx(
        'h-full flex flex-col py-3 px-2 transition-all duration-300 overflow-hidden',
        compact ? 'w-14' : 'w-56',
      )}
    >
      {/* Toggle button — hidden below `lg`, where the compact form is forced
          and a control that cannot change anything is worse than no control. */}
      {isDesktop && (
        <div className={clsx('mb-2', compact ? 'flex justify-center' : 'flex justify-end')}>
          <button
            onClick={toggleCollapsed}
            className="btn-ghost focus-ring p-1.5 rounded-lg"
            title={compact ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-label={compact ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-expanded={!compact}
          >
            {compact ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
          </button>
        </div>
      )}

      {/* Navigation */}
      <nav className="space-y-1" aria-label="Primary">
        {navItems.map((item) => {
          const count = item.badge === 'pending' ? pendingCount : 0;
          return (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) => clsx(linkClass({ isActive }), 'focus-ring')}
              title={compact ? item.label : undefined}
            >
              {item.icon}
              {!compact && <span className="flex-1">{item.label}</span>}
              {count > 0 && (
                <span
                  className="rounded-full bg-amber-500 px-1.5 text-[10px] font-bold text-white"
                  aria-label={`${count} changes awaiting review`}
                >
                  {count}
                </span>
              )}
            </NavLink>
          );
        })}
      </nav>

      <div className="divider my-3" />

      {/* Docs */}
      <div className="space-y-1">
        {!compact && (
          <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-gray-500">
            Docs
          </div>
        )}
        {docItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={docLinkClass}
            title={compact ? item.label : undefined}
          >
            {item.icon}
            {!compact && <span className="truncate">{item.label}</span>}
          </NavLink>
        ))}
      </div>

      <div className="divider my-3" />

      {/* Spacer */}
      <div className="flex-1" />

      {/* Status footer */}
      {!compact ? (
        <div className="px-3 py-2 space-y-2">
          {/* Connection status */}
          <div className="flex items-center gap-2 text-xs">
            {online ? (
              <>
                <Wifi size={12} className="text-emerald-500" aria-hidden="true" />
                <span className="text-emerald-600 dark:text-emerald-400">Connected</span>
              </>
            ) : (
              <>
                <WifiOff size={12} className="text-red-500" aria-hidden="true" />
                <span className="text-red-500">
                  {ctx?.connection === 'reconnecting' ? 'Reconnecting…' : 'Offline'}
                </span>
              </>
            )}
          </div>

          {/* Provider / Model */}
          {ctx?.config && (
            <div className="flex items-center gap-2 text-[10px] text-gray-400 dark:text-gray-500">
              <Cpu size={12} />
              <span className="truncate">{ctx.config.provider} / {ctx.config.model}</span>
            </div>
          )}

          {/* Active Pack */}
          {ctx?.config?.active_pack && (
            <div className="flex items-center gap-2 text-[10px] text-brand-600 dark:text-brand-400">
              <Package size={12} />
              <span className="truncate font-medium">Pack: {ctx.config.active_pack}</span>
            </div>
          )}

          {/* Running indicator */}
          {liveHealth?.running && (
            <div className="flex items-center gap-2 text-xs">
              <Activity size={12} className="text-brand-500 animate-pulse" />
              <span className="text-brand-600 dark:text-brand-400 animate-pulse">
                Agent running…
              </span>
            </div>
          )}
        </div>
      ) : (
        <div className="flex flex-col items-center gap-2 py-2">
          {/* Connection status icon only */}
          {online ? (
            <span title="Connected" role="status" aria-label="API connected">
              <Wifi size={14} className="text-emerald-500" aria-hidden="true" />
            </span>
          ) : (
            <span title="Offline" role="status" aria-label="API disconnected">
              <WifiOff size={14} className="text-red-500" aria-hidden="true" />
            </span>
          )}
          {/* Running indicator icon only */}
          {liveHealth?.running && (
            <span title="Agent running">
              <Activity size={14} className="text-brand-500 animate-pulse" />
            </span>
          )}
        </div>
      )}
    </div>
  );
}
