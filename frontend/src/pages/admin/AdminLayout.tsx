import React, { useState, useEffect } from 'react';
import { Folder, FolderSync, Sparkles, Users, Sliders } from 'lucide-react';
import { MediaItem } from '../../types';
import { AdminLibraries } from './AdminLibraries';
import { AdminXiaoyaSync } from './AdminXiaoyaSync';
import { AdminTmdb } from './AdminTmdb';
import { AdminSystemUsers } from './AdminSystemUsers';
import { ErrorBoundary } from '../../components/ErrorBoundary';

interface AdminLayoutProps {
  initialTab?: 'libraries' | 'sync' | 'tmdb' | 'users';
  initialMatchItem?: MediaItem | null;
  onClearInitialMatchItem?: () => void;
  onOpenLogs: () => void;
}

export const AdminLayout: React.FC<AdminLayoutProps> = ({
  initialTab = 'libraries',
  initialMatchItem,
  onClearInitialMatchItem,
  onOpenLogs,
}) => {
  const [activeTab, setActiveTab] = useState<'libraries' | 'sync' | 'tmdb' | 'users'>(initialTab);

  useEffect(() => {
    if (initialMatchItem) {
      setActiveTab('tmdb');
    }
  }, [initialMatchItem]);

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 py-6 space-y-6">
      {/* Sub-navigation Tabs */}
      <div className="flex items-center gap-2 overflow-x-auto pb-1 border-b border-zinc-800">
        <button
          type="button"
          onClick={() => setActiveTab('libraries')}
          className={`flex items-center gap-2 px-4 py-2.5 rounded-xl text-xs font-semibold transition shrink-0 cursor-pointer ${
            activeTab === 'libraries'
              ? 'bg-zinc-800 text-emerald-400 border border-zinc-700 shadow-sm'
              : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
          }`}
        >
          <Folder className="w-4 h-4" />
          媒体库管理
        </button>

        <button
          type="button"
          onClick={() => setActiveTab('sync')}
          className={`flex items-center gap-2 px-4 py-2.5 rounded-xl text-xs font-semibold transition shrink-0 cursor-pointer ${
            activeTab === 'sync'
              ? 'bg-zinc-800 text-emerald-400 border border-zinc-700 shadow-sm'
              : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
          }`}
        >
          <FolderSync className="w-4 h-4" />
          小雅同步入库 & AList
        </button>

        <button
          type="button"
          onClick={() => setActiveTab('tmdb')}
          className={`flex items-center gap-2 px-4 py-2.5 rounded-xl text-xs font-semibold transition shrink-0 cursor-pointer ${
            activeTab === 'tmdb'
              ? 'bg-zinc-800 text-emerald-400 border border-zinc-700 shadow-sm'
              : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
          }`}
        >
          <Sparkles className="w-4 h-4" />
          TMDB 刮削与识别
        </button>

        <button
          type="button"
          onClick={() => setActiveTab('users')}
          className={`flex items-center gap-2 px-4 py-2.5 rounded-xl text-xs font-semibold transition shrink-0 cursor-pointer ${
            activeTab === 'users'
              ? 'bg-zinc-800 text-emerald-400 border border-zinc-700 shadow-sm'
              : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
          }`}
        >
          <Users className="w-4 h-4" />
          系统与用户设置
        </button>
      </div>

      {/* Tab Panels */}
      <ErrorBoundary fallbackTitle="管理面板加载异常">
        <div>
          {activeTab === 'libraries' && <AdminLibraries onOpenLogs={onOpenLogs} />}
          {activeTab === 'sync' && <AdminXiaoyaSync onOpenLogs={onOpenLogs} />}
          {activeTab === 'tmdb' && (
            <AdminTmdb
              initialMatchItem={initialMatchItem}
              onClearInitialMatchItem={onClearInitialMatchItem}
            />
          )}
          {activeTab === 'users' && <AdminSystemUsers />}
        </div>
      </ErrorBoundary>
    </div>
  );
};
