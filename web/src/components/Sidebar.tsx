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
} from 'lucide-react';
import { AppContext } from '@/App';
import { getHealth } from '@/api/client';
import type { Health } from '@/types';
import clsx from 'clsx';

interface NavItem {
  to: string;
  label: string;
  icon: React.ReactNode;
}

const navItems: NavItem[] = [
  { to: '/', label: 'Live', icon: <LayoutDashboard size={18} /> },
  { to: '/board', label: 'Board', icon: <Kanban size={18} /> },
  { to: '/pipeline', label: 'Pipeline', icon: <Workflow size={18} /> },
  { to: '/agents', label: 'Agents', icon: <Bot size={18} /> },
  { to: '/blocks', label: 'Blocks', icon: <Package size={18} /> },
  { to: '/skills', label: 'Skills', icon: <Puzzle size={18} /> },
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

  const [isCollapsed, setIsCollapsed] = useState(() => {
    const stored = localStorage.getItem('slmcode-sidebar-collapsed');
    return stored === 'true';
  });

  const toggleCollapsed = () => {
    setIsCollapsed((prev) => {
      const next = !prev;
      localStorage.setItem('slmcode-sidebar-collapsed', String(next));
      return next;
    });
  };

  useEffect(() => {
    const interval = setInterval(async () => {
      try {
        const h = await getHealth();
        setLiveHealth(h);
      } catch {
        setLiveHealth(null);
      }
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  const linkClass = ({ isActive }: { isActive: boolean }) =>
    clsx(
      'flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-all duration-150',
      isActive
        ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-700 dark:text-brand-300'
        : 'text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 hover:text-gray-900 dark:hover:text-gray-100',
      isCollapsed && 'justify-center px-0 w-10 h-10 mx-auto',
    );

  const docLinkClass = ({ isActive }: { isActive: boolean }) =>
    clsx(
      'flex items-center gap-2 px-3 py-1.5 rounded-md text-xs transition-colors',
      isActive
        ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-600 dark:text-brand-400'
        : 'text-gray-500 dark:text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800',
      isCollapsed && 'justify-center px-0 w-8 h-8 mx-auto',
    );

  return (
    <div
      className={clsx(
        'h-full flex flex-col py-3 px-2 transition-all duration-300 overflow-hidden',
        isCollapsed ? 'w-14' : 'w-56',
      )}
    >
      {/* Toggle button */}
      <div className={clsx('mb-2', isCollapsed ? 'flex justify-center' : 'flex justify-end')}>
        <button
          onClick={toggleCollapsed}
          className="btn-ghost p-1.5 rounded-lg"
          title={isCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {isCollapsed ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
        </button>
      </div>

      {/* Navigation */}
      <nav className="space-y-1">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === '/'}
            className={linkClass}
            title={isCollapsed ? item.label : undefined}
          >
            {item.icon}
            {!isCollapsed && <span>{item.label}</span>}
          </NavLink>
        ))}
      </nav>

      <div className="divider my-3" />

      {/* Docs */}
      <div className="space-y-1">
        {!isCollapsed && (
          <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-gray-500">
            Docs
          </div>
        )}
        {docItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={docLinkClass}
            title={isCollapsed ? item.label : undefined}
          >
            {item.icon}
            {!isCollapsed && <span className="truncate">{item.label}</span>}
          </NavLink>
        ))}
      </div>

      <div className="divider my-3" />

      {/* Archives */}
      <NavLink
        to="/docs/ARCHIVES"
        className={docLinkClass}
        title={isCollapsed ? 'Archives' : undefined}
      >
        <Archive size={16} />
        {!isCollapsed && <span>Archives</span>}
      </NavLink>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Status footer */}
      {!isCollapsed ? (
        <div className="px-3 py-2 space-y-2">
          {/* Connection status */}
          <div className="flex items-center gap-2 text-xs">
            {liveHealth?.ok ? (
              <>
                <Wifi size={12} className="text-emerald-500" />
                <span className="text-emerald-600 dark:text-emerald-400">Connected</span>
              </>
            ) : (
              <>
                <WifiOff size={12} className="text-red-500" />
                <span className="text-red-500">Offline</span>
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
          {liveHealth?.ok ? (
            <span title="Connected">
              <Wifi size={14} className="text-emerald-500" />
            </span>
          ) : (
            <span title="Offline">
              <WifiOff size={14} className="text-red-500" />
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
