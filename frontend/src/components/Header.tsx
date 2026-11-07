import React, { useState } from 'react';
import { User } from '../types';
import { Film, Settings, Terminal, LogOut, Key, User as UserIcon, Check } from 'lucide-react';
import { changePasswordApi, notifyToast } from '../api/client';

interface HeaderProps {
  user: User;
  currentView: 'wall' | 'admin';
  onViewChange: (view: 'wall' | 'admin') => void;
  onOpenLogs: () => void;
  onLogout: () => void;
}

export const Header: React.FC<HeaderProps> = ({
  user,
  currentView,
  onViewChange,
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
      <header className="sticky top-0 z-40 w-full border-b border-zinc-800/80 bg-zinc-950/85 backdrop-blur-md">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 h-16 flex items-center justify-between gap-4">
          {/* Logo & Main Nav */}
          <div className="flex items-center gap-6">
            <div className="flex items-center gap-2.5 cursor-pointer" onClick={() => onViewChange('wall')}>
              <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-emerald-600 to-teal-400 flex items-center justify-center shadow-lg shadow-emerald-500/20">
                <Film className="w-5 h-5 text-white" />
              </div>
              <div className="flex flex-col">
                <span className="font-bold text-base tracking-tight bg-gradient-to-r from-zinc-100 to-zinc-400 bg-clip-text text-transparent">
                  小雅媒体中心
                </span>
                <span className="text-[10px] text-zinc-500 font-mono tracking-wider -mt-0.5">
                  XIAOYA MEDIA SERVER
                </span>
              </div>
            </div>

            {/* Navigation Tabs */}
            <nav className="flex items-center gap-1.5 p-1 bg-zinc-900/90 rounded-xl border border-zinc-800/80">
              <button
                onClick={() => onViewChange('wall')}
                className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all ${
                  currentView === 'wall'
                    ? 'bg-zinc-800 text-emerald-400 shadow-sm'
                    : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
                }`}
              >
                <Film className="w-4 h-4" />
                影视墙
              </button>
              {isAdmin && (
                <button
                  onClick={() => onViewChange('admin')}
                  className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-sm font-medium transition-all ${
                    currentView === 'admin'
                      ? 'bg-zinc-800 text-emerald-400 shadow-sm'
                      : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
                  }`}
                >
                  <Settings className="w-4 h-4" />
                  管理控制台
                </button>
              )}
            </nav>
          </div>

          {/* Right Action Tools */}
          <div className="flex items-center gap-3">
            {/* Full Chain Log Button */}
            <button
              onClick={onOpenLogs}
              className="group flex items-center gap-2 px-3 py-1.5 rounded-xl border border-zinc-800 bg-zinc-900/60 hover:bg-zinc-800/80 hover:border-zinc-700 text-zinc-300 hover:text-white transition text-xs font-mono"
              title="查看全链路运行日志"
            >
              <div className="relative">
                <Terminal className="w-4 h-4 text-emerald-400" />
                <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              </div>
              <span className="hidden sm:inline">全链路日志</span>
            </button>

            {/* User Dropdown */}
            <div className="relative">
              <button
                onClick={() => setShowUserMenu(!showUserMenu)}
                className="flex items-center gap-2.5 px-3 py-1.5 rounded-xl border border-zinc-800 bg-zinc-900/60 hover:bg-zinc-800/80 text-zinc-200 transition text-sm"
              >
                <div className="w-6 h-6 rounded-lg bg-emerald-950 border border-emerald-500/30 text-emerald-400 flex items-center justify-center font-bold text-xs">
                  {user.Name.slice(0, 1).toUpperCase()}
                </div>
                <span className="font-medium max-w-[100px] truncate">{user.Name}</span>
              </button>

              {showUserMenu && (
                <>
                  <div
                    className="fixed inset-0 z-40"
                    onClick={() => setShowUserMenu(false)}
                  />
                  <div className="absolute right-0 mt-2 w-48 rounded-xl bg-zinc-900 border border-zinc-800 shadow-2xl py-1.5 z-50 animate-in fade-in zoom-in-95">
                    <div className="px-3 py-2 border-b border-zinc-800/80 text-xs text-zinc-400">
                      用户身份: <span className="text-zinc-200 font-semibold">{isAdmin ? '超级管理员' : '普通用户'}</span>
                    </div>
                    <button
                      onClick={() => {
                        setShowUserMenu(false);
                        setShowPasswordModal(true);
                      }}
                      className="w-full flex items-center gap-2 px-3 py-2 text-sm text-zinc-300 hover:text-white hover:bg-zinc-800/70 transition"
                    >
                      <Key className="w-4 h-4 text-zinc-400" />
                      修改密码
                    </button>
                    <button
                      onClick={() => {
                        setShowUserMenu(false);
                        onLogout();
                      }}
                      className="w-full flex items-center gap-2 px-3 py-2 text-sm text-rose-400 hover:text-rose-300 hover:bg-rose-950/30 transition"
                    >
                      <LogOut className="w-4 h-4" />
                      退出登录
                    </button>
                  </div>
                </>
              )}
            </div>
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
