import { Outlet } from 'react-router-dom';
import TopBar from './TopBar';
import Sidebar from './Sidebar';

export default function Layout() {
  return (
    <div className="h-screen flex flex-col overflow-hidden">
      <TopBar />

      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar — manages its own collapsed/expanded width */}
        <aside className="flex-shrink-0 border-r border-gray-200 dark:border-gray-800 bg-surface-alt">
          <Sidebar />
        </aside>

        {/* Main content */}
        <main className="flex-1 overflow-auto bg-surface">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
