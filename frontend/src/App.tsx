import React, { useState, useEffect } from 'react';
import { User, MediaItem } from './types';
import { getStoredUser, getStoredToken, logoutApi, fetchCurrentUser } from './api/client';
import { Header } from './components/Header';
import { LoginView } from './components/LoginView';
import { MediaWall } from './pages/MediaWall';
import { AdminLayout } from './pages/admin/AdminLayout';
import { LogDrawer } from './components/LogDrawer';
import { ToastContainer } from './components/Toast';
import { APP_VERSION, BUILD_COMMIT, BUILD_TIME } from './version';

export const App: React.FC = () => {
  const [currentUser, setCurrentUser] = useState<User | null>(getStoredUser());
  const [currentView, setCurrentView] = useState<'wall' | 'admin'>(() => {
    const user = getStoredUser();
    if (window.location.pathname.startsWith('/web/manage') && (user?.Policy?.IsAdministrator ?? false)) {
      return 'admin';
    }
    return 'wall';
  });
  const [activeTab, setActiveTab] = useState<'home' | 'favorites'>('home');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [showSearchModal, setShowSearchModal] = useState<boolean>(false);
  const [showLogs, setShowLogs] = useState<boolean>(false);
  const [matchTargetItem, setMatchTargetItem] = useState<MediaItem | null>(null);

  useEffect(() => {
    const handleAuthExpired = () => {
      setCurrentUser(null);
    };
    window.addEventListener('auth:expired', handleAuthExpired);
    return () => window.removeEventListener('auth:expired', handleAuthExpired);
  }, []);

  useEffect(() => {
    if (!currentUser || !getStoredToken()) return;
    fetchCurrentUser(currentUser.Id)
      .then((serverUser) => setCurrentUser(serverUser))
      .catch(() => {
        // The request helper handles expired sessions; ordinary profile errors
        // leave the current display view available while management APIs stay
        // server-authorized.
      });
  }, []);

  useEffect(() => {
    const onPopState = () => setCurrentView(window.location.pathname.startsWith('/web/manage') ? 'admin' : 'wall');
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const navigate = (view: 'wall' | 'admin') => {
    if (view === 'admin' && !(currentUser?.Policy?.IsAdministrator ?? false)) {
      window.history.replaceState({}, '', '/web/');
      setCurrentView('wall');
      return;
    }
    const path = view === 'admin' ? '/web/manage/' : '/web/';
    if (window.location.pathname !== path) window.history.pushState({}, '', path);
    setCurrentView(view);
  };

  useEffect(() => {
    if (currentView === 'admin' && !(currentUser?.Policy?.IsAdministrator ?? false)) {
      navigate('wall');
    }
  }, [currentUser, currentView]);

  const handleLogout = async () => {
    await logoutApi();
    setCurrentUser(null);
  };

  const handleRematchTmdbFromWall = (item: MediaItem) => {
    if (!(currentUser?.Policy?.IsAdministrator ?? false)) return;
    setMatchTargetItem(item);
    navigate('admin');
  };

  if (!currentUser || !getStoredToken()) {
    return (
      <>
        <LoginView onSuccess={(user) => setCurrentUser(user)} />
        <ToastContainer />
      </>
    );
  }

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100 flex flex-col">
      <Header
        user={currentUser}
        currentView={currentView}
        activeTab={activeTab}
        onTabChange={(tab) => {
          setActiveTab(tab);
          if (currentView !== 'wall') navigate('wall');
        }}
        onViewChange={navigate}
        onOpenSearch={() => setShowSearchModal(true)}
        onOpenLogs={() => setShowLogs(true)}
        onLogout={handleLogout}
      />

      {/* Global Quick Search Overlay / Modal */}
      {showSearchModal && (
        <div className="fixed inset-0 z-50 flex items-start justify-center pt-20 px-4 bg-black/80 backdrop-blur-md animate-in fade-in">
          <div className="w-full max-w-xl bg-zinc-900 border border-zinc-800 rounded-3xl p-5 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-zinc-800 pb-3">
              <span className="font-bold text-white text-sm">全局影视搜索 (支持汉字、拼音首字母如 zmsr)</span>
              <button
                type="button"
                onClick={() => setShowSearchModal(false)}
                className="text-xs px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300"
              >
                关闭
              </button>
            </div>
            <div className="flex items-center gap-2">
              <input
                type="text"
                autoFocus
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="输入影片名或拼音首字母检索..."
                className="flex-1 px-4 py-2.5 rounded-2xl bg-zinc-950 border border-zinc-700 text-white text-sm focus:outline-none focus:border-emerald-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') setShowSearchModal(false);
                }}
              />
              <button
                type="button"
                onClick={() => setShowSearchModal(false)}
                className="px-4 py-2.5 rounded-2xl bg-emerald-500 hover:bg-emerald-400 text-white font-bold text-sm"
              >
                搜索
              </button>
            </div>
            {searchQuery && (
              <div className="flex items-center justify-between text-xs text-zinc-400">
                <span>当前搜索关键词: <b className="text-emerald-400">{searchQuery}</b></span>
                <button
                  type="button"
                  onClick={() => setSearchQuery('')}
                  className="text-zinc-500 hover:text-zinc-300 underline"
                >
                  清空
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      <main className="flex-1">
        {currentView === 'wall' ? (
          <MediaWall
            user={currentUser}
            activeTab={activeTab}
            searchQuery={searchQuery}
            onRematchTmdb={handleRematchTmdbFromWall}
          />
        ) : currentUser.Policy?.IsAdministrator ? (
          <AdminLayout
            initialMatchItem={matchTargetItem}
            onClearInitialMatchItem={() => setMatchTargetItem(null)}
            onOpenLogs={() => setShowLogs(true)}
          />
        ) : (
          <div className="max-w-3xl mx-auto px-6 py-20 text-center text-zinc-400">没有管理权限</div>
        )}
      </main>

      <LogDrawer isOpen={showLogs} onClose={() => setShowLogs(false)} />
      <ToastContainer />

      <footer className="py-5 border-t border-zinc-900 bg-zinc-950/60 text-center text-xs text-zinc-500 font-mono">
        <div className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1">
          <span className="text-zinc-400 font-medium">小雅媒体中心 Xiaoya Media Server</span>
          <span className="text-zinc-700">•</span>
          <span className="text-zinc-300 font-semibold">{APP_VERSION}</span>
          <span className="text-zinc-700">•</span>
          <span className="text-emerald-400 font-mono">Build: {BUILD_COMMIT}</span>
          <span className="text-zinc-700">•</span>
          <span className="text-zinc-500">{BUILD_TIME}</span>
        </div>
      </footer>
    </div>
  );
};
