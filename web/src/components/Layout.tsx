import { Outlet } from 'react-router-dom';
import TopBar from './TopBar';
import Sidebar from './Sidebar';
import { useState } from 'react';
import clsx from 'clsx';
import { PanelLeftClose, PanelLeft } from 'lucide-react';

export default function Layout() {
  const [sidebarOpen, setSidebarOpen] = useState(true);

  return (
    <div className="h-screen flex flex-col overflow-hidden">
      <TopBar onToggleSidebar={() => setSidebarOpen((o) => !o)} sidebarOpen={sidebarOpen} />

      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar */}
        <aside
          className={clsx(
            'flex-shrink-0 border-r border-gray-200 dark:border-gray-800 bg-surface-alt transition-all duration-300 overflow-hidden',
            sidebarOpen ? 'w-56' : 'w-0 border-0',
          )}
        >
          <div className="w-56 h-full">
            <Sidebar />
          </div>
        </aside>

        {/* Sidebar toggle when closed */}
        {!sidebarOpen && (
          <button
            onClick={() => setSidebarOpen(true)}
            className="absolute left-2 top-[4.5rem] z-10 btn-ghost p-2 rounded-lg"
            title="Open sidebar"
          >
            <PanelLeft size={18} />
          </button>
        )}

        {/* Main content */}
        <main className="flex-1 overflow-auto bg-surface">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
