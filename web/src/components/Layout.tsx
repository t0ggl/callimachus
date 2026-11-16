import React from 'react';
import {
  Library,
  Activity,
  Send,
  Settings,
  Search,
  LogOut,
  Zap,
  DownloadCloud,
  ShieldCheck,
} from 'lucide-react';
import { UserProfile } from '../types';

export type NavView = 'catalog' | 'activity' | 'requests' | 'settings';

interface LayoutProps {
  currentView: NavView;
  onNavigate: (view: NavView) => void;
  onOpenPalette: () => void;
  user: UserProfile | null;
  activeDownloadsCount: number;
  transcodingJobsCount: number;
  onLogout: () => void;
  children: React.ReactNode;
}

export const Layout: React.FC<LayoutProps> = ({
  currentView,
  onNavigate,
  onOpenPalette,
  user,
  activeDownloadsCount,
  transcodingJobsCount,
  onLogout,
  children,
}) => {
  const navItems = [
    {
      id: 'catalog' as NavView,
      label: 'Library & Catalog',
      icon: Library,
      shortcut: '1',
    },
    {
      id: 'activity' as NavView,
      label: 'Activity & Queues',
      icon: Activity,
      badge: transcodingJobsCount > 0 || activeDownloadsCount > 0 ? `${transcodingJobsCount + activeDownloadsCount}` : undefined,
      shortcut: '2',
    },
    {
      id: 'requests' as NavView,
      label: 'Request Portal',
      icon: Send,
      shortcut: '3',
    },
    {
      id: 'settings' as NavView,
      label: 'Settings',
      icon: Settings,
      shortcut: '4',
    },
  ];

  return (
    <div className="flex h-screen bg-zinc-950 text-zinc-100 overflow-hidden">
      {/* Sidebar Navigation */}
      <aside className="w-64 bg-zinc-900/90 border-r border-zinc-800 flex flex-col justify-between shrink-0">
        <div>
          {/* Logo & Brand Header */}
          <div className="h-14 flex items-center px-4 border-b border-zinc-800 gap-3">
            <div className="w-8 h-8 rounded-lg bg-blue-600/20 border border-blue-500/40 flex items-center justify-center text-blue-400">
              <svg className="w-5 h-5" viewBox="0 0 32 32" fill="none" aria-hidden="true">
                <rect x="5" y="13" width="5.5" height="14" rx="1.5" fill="#3B82F6" fillOpacity="0.25" stroke="#60A5FA" strokeWidth="2.2" />
                <rect x="12.5" y="10" width="5.5" height="17" rx="1.5" fill="#3B82F6" fillOpacity="0.4" stroke="#60A5FA" strokeWidth="2.2" />
                <rect x="20.5" y="14.5" width="5.5" height="12.5" rx="1.5" transform="rotate(-10 20.5 14.5)" fill="#38BDF8" fillOpacity="0.25" stroke="#38BDF8" strokeWidth="2.2" />
                <path d="M19 6.5C21.8 6.5 24.2 8.2 25.2 10.7" stroke="#93C5FD" strokeWidth="2.2" strokeLinecap="round" />
                <path d="M20.5 3.5C24.5 3.8 27.8 6.6 28.8 10.5" stroke="#38BDF8" strokeWidth="2.2" strokeLinecap="round" />
              </svg>
            </div>
            <div>
              <div className="text-sm font-bold tracking-tight text-white flex items-center gap-1.5">
                Callimachus
                <span className="text-[10px] font-mono px-1 py-0.2 rounded bg-zinc-800 text-zinc-400 border border-zinc-700">
                  v0.1
                </span>
              </div>
              <div className="text-[11px] text-zinc-400">Audiobooks & eBooks</div>
            </div>
          </div>

          {/* Navigation Links */}
          <nav className="p-3 space-y-1">
            {navItems.map((item) => {
              const Icon = item.icon;
              const isActive = currentView === item.id;
              return (
                <button
                  key={item.id}
                  onClick={() => onNavigate(item.id)}
                  data-testid={`nav-${item.id}`}
                  className={`w-full flex items-center justify-between px-3 py-2 rounded-md text-xs font-medium transition-colors duration-150 ${
                    isActive
                      ? 'bg-blue-600/15 text-blue-400 border border-blue-600/30'
                      : 'text-zinc-400 hover:text-zinc-100 hover:bg-zinc-800/60 border border-transparent'
                  }`}
                >
                  <div className="flex items-center gap-2.5">
                    <Icon className={`w-4 h-4 ${isActive ? 'text-blue-400' : 'text-zinc-400'}`} />
                    <span>{item.label}</span>
                  </div>

                  {item.badge && (
                    <span className="text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded-full bg-blue-500/20 text-blue-300 border border-blue-500/30">
                      {item.badge}
                    </span>
                  )}
                </button>
              );
            })}
          </nav>
        </div>

        {/* Sidebar Footer: System Status */}
        <div className="p-3 border-t border-zinc-800 space-y-3 bg-zinc-950/40">
          <div className="space-y-1.5 text-[11px]">
            <div className="flex items-center justify-between text-zinc-400">
              <span className="flex items-center gap-1.5">
                <Zap className="w-3.5 h-3.5 text-amber-400" />
                Transcode Queue
              </span>
              <span className="font-mono text-zinc-300">{transcodingJobsCount} active</span>
            </div>
            <div className="flex items-center justify-between text-zinc-400">
              <span className="flex items-center gap-1.5">
                <DownloadCloud className="w-3.5 h-3.5 text-cyan-400" />
                Downloads
              </span>
              <span className="font-mono text-zinc-300">{activeDownloadsCount} active</span>
            </div>
          </div>

          {/* User info & Logout */}
          <div className="pt-2 border-t border-zinc-800 flex items-center justify-between">
            {user ? (
              <>
                <div className="flex items-center gap-2 min-w-0">
                  <div className="w-6 h-6 rounded-full bg-zinc-800 border border-zinc-700 flex items-center justify-center text-[11px] font-semibold text-zinc-300 uppercase">
                    {(user.username || 'U').charAt(0)}
                  </div>
                  <div className="truncate">
                    <div className="text-xs font-medium text-zinc-200 truncate">{user.username}</div>
                    <div className="text-[10px] text-zinc-400 capitalize flex items-center gap-1">
                      <ShieldCheck className="w-3 h-3 text-emerald-400" />
                      {user.role}
                    </div>
                  </div>
                </div>

                <button
                  onClick={onLogout}
                  data-testid="logout-button"
                  className="p-1.5 text-zinc-400 hover:text-red-400 hover:bg-zinc-800/80 rounded transition-colors"
                  title="Sign Out"
                >
                  <LogOut className="w-4 h-4" />
                </button>
              </>
            ) : (
              <div
                data-testid="auth-verifying"
                className="w-full py-1.5 px-2 text-zinc-500 text-xs flex items-center gap-2"
              >
                <ShieldCheck className="w-3.5 h-3.5 text-zinc-600" />
                <span>Verifying session...</span>
              </div>
            )}
          </div>
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Top Header Bar */}
        <header className="h-14 bg-zinc-900 border-b border-zinc-800 flex items-center justify-between px-6 shrink-0">
          {/* Global Search / Command Palette Trigger */}
          <button
            onClick={onOpenPalette}
            data-testid="global-search-trigger"
            className="flex items-center justify-between w-72 sm:w-96 px-3 py-1.5 bg-zinc-950 border border-zinc-800 rounded-md text-xs text-zinc-400 hover:text-zinc-200 hover:border-zinc-700 transition-colors shadow-inner"
          >
            <div className="flex items-center gap-2">
              <Search className="w-3.5 h-3.5 text-zinc-500" />
              <span>Search catalog or commands...</span>
            </div>
            <div className="flex items-center gap-1 font-mono text-[10px] text-zinc-500">
              <kbd className="px-1.5 py-0.5 bg-zinc-800 border border-zinc-700 rounded">Ctrl+K</kbd>
              <span className="text-zinc-600">or</span>
              <kbd className="px-1.5 py-0.5 bg-zinc-800 border border-zinc-700 rounded">/</kbd>
            </div>
          </button>

          {/* Quick status bar items */}
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 text-xs">
              <span
                onClick={() => onNavigate('activity')}
                className="cursor-pointer flex items-center gap-1.5 px-2.5 py-1 rounded bg-zinc-800/60 hover:bg-zinc-800 text-zinc-300 border border-zinc-700/60"
                title="Active Transcodes"
              >
                <Zap className="w-3.5 h-3.5 text-amber-400" />
                <span className="font-mono text-[11px]">{transcodingJobsCount}</span>
              </span>
              <span
                onClick={() => onNavigate('activity')}
                className="cursor-pointer flex items-center gap-1.5 px-2.5 py-1 rounded bg-zinc-800/60 hover:bg-zinc-800 text-zinc-300 border border-zinc-700/60"
                title="Active Downloads"
              >
                <DownloadCloud className="w-3.5 h-3.5 text-cyan-400" />
                <span className="font-mono text-[11px]">{activeDownloadsCount}</span>
              </span>
            </div>

            <div className="h-4 w-px bg-zinc-800" />

            <div className="flex items-center gap-2">
              <span className="text-xs font-mono px-2 py-0.5 bg-emerald-950/60 text-emerald-400 border border-emerald-800/60 rounded">
                OPERATE MODE
              </span>
            </div>
          </div>
        </header>

        {/* View Viewport */}
        <main className="flex-1 overflow-y-auto bg-zinc-950 p-6">
          {children}
        </main>
      </div>
    </div>
  );
};
