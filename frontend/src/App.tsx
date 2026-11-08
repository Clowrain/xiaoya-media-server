import React, { useState, useEffect } from 'react';
import { User, MediaItem } from './types';
import { getStoredUser, getStoredToken, logoutApi, fetchCurrentUser } from './api/client';
import { Header } from './components/Header';
import { LoginView } from './components/LoginView';
import { MediaWall } from './pages/MediaWall';
import { AdminLayout } from './pages/admin/AdminLayout';
import { LogDrawer } from './components/LogDrawer';
import { ToastContainer } from './components/Toast';

export const App: React.FC = () => {
  const [currentUser, setCurrentUser] = useState<User | null>(getStoredUser());
  const [currentView, setCurrentView] = useState<'wall' | 'admin'>(() => {
    const user = getStoredUser();
    if (window.location.pathname.startsWith('/web/manage') && (user?.Policy?.IsAdministrator ?? false)) {
      return 'admin';
    }
    return 'wall';
  });
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
        onViewChange={navigate}
        onOpenLogs={() => setShowLogs(true)}
        onLogout={handleLogout}
      />

      <main className="flex-1">
        {currentView === 'wall' ? (
          <MediaWall
            user={currentUser}
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
    </div>
  );
};
