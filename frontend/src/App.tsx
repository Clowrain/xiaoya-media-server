import React, { useState, useEffect } from 'react';
import { User, MediaItem } from './types';
import { getStoredUser, getStoredToken, logoutApi } from './api/client';
import { Header } from './components/Header';
import { LoginView } from './components/LoginView';
import { MediaWall } from './pages/MediaWall';
import { AdminLayout } from './pages/admin/AdminLayout';
import { LogDrawer } from './components/LogDrawer';
import { ToastContainer } from './components/Toast';

export const App: React.FC = () => {
  const [currentUser, setCurrentUser] = useState<User | null>(getStoredUser());
  const [currentView, setCurrentView] = useState<'wall' | 'admin'>('wall');
  const [showLogs, setShowLogs] = useState<boolean>(false);
  const [matchTargetItem, setMatchTargetItem] = useState<MediaItem | null>(null);

  useEffect(() => {
    const handleAuthExpired = () => {
      setCurrentUser(null);
    };
    window.addEventListener('auth:expired', handleAuthExpired);
    return () => window.removeEventListener('auth:expired', handleAuthExpired);
  }, []);

  const handleLogout = async () => {
    await logoutApi();
    setCurrentUser(null);
  };

  const handleRematchTmdbFromWall = (item: MediaItem) => {
    setMatchTargetItem(item);
    setCurrentView('admin');
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
        onViewChange={(v) => setCurrentView(v)}
        onOpenLogs={() => setShowLogs(true)}
        onLogout={handleLogout}
      />

      <main className="flex-1">
        {currentView === 'wall' ? (
          <MediaWall
            user={currentUser}
            onRematchTmdb={handleRematchTmdbFromWall}
          />
        ) : (
          <AdminLayout
            initialMatchItem={matchTargetItem}
            onClearInitialMatchItem={() => setMatchTargetItem(null)}
            onOpenLogs={() => setShowLogs(true)}
          />
        )}
      </main>

      <LogDrawer isOpen={showLogs} onClose={() => setShowLogs(false)} />
      <ToastContainer />
    </div>
  );
};
