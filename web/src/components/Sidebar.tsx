import { useContext, useEffect, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
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
  const location = useLocation();
  const [liveHealth, setLiveHealth] = useState<Health | null>(null);

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
    );

  const docLinkClass = ({ isActive }: { isActive: boolean }) =>
    clsx(
      'flex items-center gap-2 px-3 py-1.5 rounded-md text-xs transition-colors',
      isActive
        ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-600 dark:text-brand-400'
        : 'text-gray-500 dark:text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800',
    );

  return (
    <div className="h-full flex flex-col py-3 px-2">
      {/* Navigation */}
      <nav className="space-y-1">
        {navItems.map((item) => (
          <NavLink key={item.to} to={item.to} end={item.to === '/'} className={linkClass}>
            {item.icon}
            <span>{item.label}</span>
          </NavLink>
        ))}
      </nav>

      <div className="divider my-3" />

      {/* Docs */}
      <div className="space-y-1">
        <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-gray-500">
          Docs
        </div>
        {docItems.map((item) => (
          <NavLink key={item.to} to={item.to} className={docLinkClass}>
            {item.icon}
            <span className="truncate">{item.label}</span>
          </NavLink>
        ))}
      </div>

      <div className="divider my-3" />

      {/* Archives */}
      <NavLink to="/docs/ARCHIVES" className={docLinkClass}>
        <Archive size={16} />
        <span>Archives</span>
      </NavLink>

      {/* Spacer */}
      <div className="flex-1" />

      {/* Status footer */}
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
    </div>
  );
}
