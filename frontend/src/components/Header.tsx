import React, { useState } from 'react';
import { User } from '../types';
import { 
  Film, Settings, Terminal, LogOut, Key, User as UserIcon, 
  Search, Cast, Menu, Play, Star 
} from 'lucide-react';
import { notifyToast } from '../api/client';
import { changePasswordApi } from '../api/users';
import { 
  APP_VERSION, BUILD_COMMIT, BUILD_TIME, 
  GITHUB_REPO_URL, getCommitUrl, formatCommitHash 
} from '../version';

interface HeaderProps {
  user: User;
  currentView: 'wall' | 'admin';
  activeTab?: 'home' | 'favorites';
  onTabChange?: (tab: 'home' | 'favorites') => void;
  onViewChange: (view: 'wall' | 'admin') => void;
  onOpenSearch?: () => void;
  onOpenLogs: () => void;
  onLogout: () => void;
}

export const Header: React.FC<HeaderProps> = ({
  user,
  currentView,
  activeTab = 'home',
  onTabChange,
  onViewChange,
  onOpenSearch,
  onOpenLogs,
  onLogout,
}) => {
  const [showUserMenu, setShowUserMenu] = useState(false);
  const [showPasswordModal, setShowPasswordModal] = useState(false);
  const [oldPw, setOldPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [isChanging, setIsChanging] = useState(false);

  const isAdmin = user.Policy?.IsAdministrator ?? false;

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPw) {
      notifyToast('新密码不能为空', 'error');
      return;
    }
    setIsChanging(true);
    try {
      await changePasswordApi(user.Id, oldPw, newPw);
      notifyToast('密码已成功修改', 'success');
      setShowPasswordModal(false);
      setOldPw('');
      setNewPw('');
    } catch (err: any) {
      notifyToast(err.message || '修改密码失败', 'error');
    } finally {
      setIsChanging(false);
    }
  };

  return (
    <>
      <header className="sticky top-0 z-40 w-full border-b border-zinc-800/80 bg-zinc-950/90 backdrop-blur-md">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 h-16 flex items-center justify-between gap-4">
          {/* Left: Emby Brand & Logo */}
          <div className="flex items-center gap-3 sm:gap-4">
            <button 
              type="button"
              className="p-1.5 rounded-lg text-zinc-400 hover:text-white hover:bg-zinc-800/60 transition cursor-pointer"
              title="主菜单"
              onClick={() => {
                if (currentView === 'admin') onViewChange('wall');
              }}
            >
              <Menu className="w-5 h-5" />
            </button>

            <div 
              className="flex items-center gap-2 cursor-pointer select-none"
              onClick={() => onViewChange('wall')}
            >
              <div className="w-7 h-7 rounded-lg bg-emerald-500 flex items-center justify-center shadow-md shadow-emerald-500/30">
                <Play className="w-4 h-4 text-black fill-black ml-0.5" />
              </div>
              <span className="font-black text-xl tracking-tight text-white flex items-center gap-1.5">
                emby
                <a 
                  href={getCommitUrl(BUILD_COMMIT)}
                  target="_blank"
                  rel="noreferrer"
                  onClick={(e) => e.stopPropagation()}
                  className="px-1.5 py-0.5 rounded-md bg-emerald-950/80 border border-emerald-500/30 text-emerald-400 hover:text-white font-mono text-[9px] font-bold tracking-tight hover:underline"
                  title={`版本: ${APP_VERSION} (Commit: ${BUILD_COMMIT}) · 点击查看提交记录`}
                >
                  {APP_VERSION} <span className="opacity-75">({formatCommitHash(BUILD_COMMIT)})</span>
                </a>
              </span>
            </div>
          </div>

          {/* Middle: Emby Style Capsule Pill [ 首页 ] [ 收藏 ] */}
          {currentView === 'wall' && onTabChange && (
            <div className="flex items-center bg-zinc-900/90 p-1 rounded-full border border-zinc-800 shadow-inner">
              <button
                type="button"
                onClick={() => onTabChange('home')}
                className={`px-4 py-1 rounded-full text-xs font-bold transition-all cursor-pointer ${
                  activeTab === 'home'
                    ? 'bg-zinc-600/90 text-white shadow-sm'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
              >
                首页
              </button>
              <button
                type="button"
                onClick={() => onTabChange('favorites')}
                className={`px-4 py-1 rounded-full text-xs font-bold transition-all cursor-pointer ${
                  activeTab === 'favorites'
                    ? 'bg-zinc-600/90 text-white shadow-sm'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
              >
                收藏
              </button>
            </div>
          )}

          {/* Right Action Tools */}
          <div className="flex items-center gap-2 sm:gap-3">
            {/* Cast Button */}
            <button
              type="button"
              className="p-2 rounded-xl text-zinc-400 hover:text-white hover:bg-zinc-800/60 transition cursor-pointer"
              title="投屏播放"
              onClick={() => notifyToast('局域网 DLNA / AirPlay 投屏准备就绪', 'info')}
            >
              <Cast className="w-5 h-5" />
            </button>

            {/* Search Button */}
            {onOpenSearch && (
              <button
                type="button"
                onClick={onOpenSearch}
                className="p-2 rounded-xl text-zinc-400 hover:text-white hover:bg-zinc-800/60 transition cursor-pointer"
                title="搜索影视 (支持拼音首字母)"
              >
                <Search className="w-5 h-5" />
              </button>
            )}

            {/* User Dropdown */}
            <div className="relative">
              <button
                type="button"
                onClick={() => setShowUserMenu(!showUserMenu)}
                className="p-2 rounded-xl text-zinc-400 hover:text-white hover:bg-zinc-800/60 transition cursor-pointer"
                title={`当前账号: ${user.Name}`}
              >
                <UserIcon className="w-5 h-5" />
              </button>

              {showUserMenu && (
                <>
                  <div
                    className="fixed inset-0 z-40"
                    onClick={() => setShowUserMenu(false)}
                  />
                  <div className="absolute right-0 mt-2 w-48 rounded-2xl bg-zinc-900 border border-zinc-800 shadow-2xl py-1.5 z-50 animate-in fade-in zoom-in-95">
                    <div className="px-3.5 py-2.5 border-b border-zinc-800/80">
                      <div className="text-sm font-bold text-white truncate">{user.Name}</div>
                      <div className="text-[11px] text-zinc-400 mt-0.5">
                        {isAdmin ? '超级管理员' : '普通观影用户'}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => {
                        setShowUserMenu(false);
                        setShowPasswordModal(true);
                      }}
                      className="w-full flex items-center gap-2 px-3.5 py-2 text-xs text-zinc-300 hover:text-white hover:bg-zinc-800/70 transition cursor-pointer"
                    >
                      <Key className="w-4 h-4 text-zinc-400" />
                      修改登录密码
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        setShowUserMenu(false);
                        onLogout();
                      }}
                      className="w-full flex items-center gap-2 px-3.5 py-2 text-xs text-rose-400 hover:text-rose-300 hover:bg-rose-950/30 transition cursor-pointer"
                    >
                      <LogOut className="w-4 h-4" />
                      退出登录
                    </button>
                    <div className="px-3 py-1.5 border-t border-zinc-800/80 text-[10px] text-zinc-500 font-mono text-center">
                      {APP_VERSION} ({BUILD_COMMIT})
                    </div>
                  </div>
                </>
              )}
            </div>

            {/* Admin Settings Gear - STRICTLY ADMIN ONLY */}
            {isAdmin && (
              <button
                type="button"
                onClick={() => onViewChange(currentView === 'admin' ? 'wall' : 'admin')}
                className={`p-2 rounded-xl transition cursor-pointer ${
                  currentView === 'admin'
                    ? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/40 shadow-sm'
                    : 'text-zinc-400 hover:text-white hover:bg-zinc-800/60'
                }`}
                title={currentView === 'admin' ? '返回影视首页' : '管理控制台 (监控目录/日志/系统配置)'}
              >
                <Settings className={`w-5 h-5 ${currentView === 'admin' ? 'animate-spin-slow' : ''}`} />
              </button>
            )}
          </div>
        </div>
      </header>

      {/* Change Password Modal */}
      {showPasswordModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4">
          <div className="w-full max-w-sm rounded-2xl bg-zinc-900 border border-zinc-800 shadow-2xl p-6">
            <h3 className="text-lg font-bold text-zinc-100 flex items-center gap-2 mb-4">
              <Key className="w-5 h-5 text-emerald-400" />
              修改密码
            </h3>
            <form onSubmit={handlePasswordSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-medium text-zinc-400 mb-1.5">当前密码 (若未设可留空)</label>
                <input
                  type="password"
                  value={oldPw}
                  onChange={(e) => setOldPw(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500 transition"
                  placeholder="请输入当前密码"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-zinc-400 mb-1.5">新密码</label>
                <input
                  type="password"
                  required
                  value={newPw}
                  onChange={(e) => setNewPw(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500 transition"
                  placeholder="请输入新密码"
                />
              </div>
              <div className="flex items-center justify-end gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => setShowPasswordModal(false)}
                  className="px-4 py-2 rounded-xl border border-zinc-800 hover:bg-zinc-800 text-zinc-400 hover:text-zinc-200 text-sm transition"
                >
                  取消
                </button>
                <button
                  type="submit"
                  disabled={isChanging}
                  className="px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-sm transition disabled:opacity-50"
                >
                  {isChanging ? '保存中…' : '确认修改'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </>
  );
};
