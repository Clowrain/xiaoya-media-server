import React, { useState, useEffect } from 'react';
import { 
  Users, UserPlus, Trash2, Edit2, Shield, Key, 
  Tv, Cpu, Sliders, Check, X 
} from 'lucide-react';
import { 
  fetchUsers, createUser, updateUser, updateUserIdentity, 
  deleteUser, fetchEnhancements, updateEnhancements, 
  fetchMediaInfoSettings, updateMediaInfoSettings, notifyToast 
} from '../../api/client';

export const AdminSystemUsers: React.FC = () => {
  const [users, setUsers] = useState<any[]>([]);
  const [enhancements, setEnhancements] = useState<any>({});
  const [mediaInfo, setMediaInfo] = useState<any>({});
  const [loading, setLoading] = useState(true);

  // User Modals
  const [showAddUser, setShowAddUser] = useState(false);
  const [addName, setAddName] = useState('');
  const [addPassword, setAddPassword] = useState('');
  const [addMaxDevices, setAddMaxDevices] = useState(5);
  const [addAllowPlayback, setAddAllowPlayback] = useState(true);

  const [editingUser, setEditingUser] = useState<any | null>(null);
  const [editPassword, setEditPassword] = useState('');
  const [editMaxDevices, setEditMaxDevices] = useState(5);
  const [editAllowPlayback, setEditAllowPlayback] = useState(true);

  const loadData = async () => {
    setLoading(true);
    try {
      const [u, enh, med] = await Promise.all([
        fetchUsers(),
        fetchEnhancements(),
        fetchMediaInfoSettings(),
      ]);
      setUsers(u || []);
      setEnhancements(enh || {});
      setMediaInfo(med || {});
    } catch (err: any) {
      notifyToast(err.message || '加载系统设置失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const handleAddUserSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!addName.trim()) return;
    try {
      await createUser({
        Name: addName.trim(),
        Password: addPassword,
        MaxDevices: addMaxDevices,
        AllowPlayback: addAllowPlayback,
      });
      notifyToast(`用户「${addName}」创建成功`, 'success');
      setShowAddUser(false);
      setAddName('');
      setAddPassword('');
      loadData();
    } catch (err: any) {
      notifyToast(err.message || '创建用户失败', 'error');
    }
  };

  const handleEditUserSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingUser) return;
    try {
      if (editPassword.trim()) {
        await updateUserIdentity(editingUser.Id, editingUser.Name, editPassword.trim());
      }
      await updateUser(editingUser.Id, {
        MaxDevices: editMaxDevices,
        Admin: editingUser.Policy?.IsAdministrator ?? false,
        AllowPlayback: editAllowPlayback,
      });
      notifyToast('用户信息已更新', 'success');
      setEditingUser(null);
      loadData();
    } catch (err: any) {
      notifyToast(err.message || '更新用户失败', 'error');
    }
  };

  const handleDeleteUser = async (id: string, name: string) => {
    if (!confirm(`确定删除用户「${name}」？`)) return;
    try {
      await deleteUser(id);
      notifyToast(`用户「${name}」已删除`, 'success');
      loadData();
    } catch (err: any) {
      notifyToast(err.message || '删除用户失败', 'error');
    }
  };

  const handleSaveEnhancements = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await updateEnhancements(enhancements);
      notifyToast('播放与性能增强设置已保存', 'success');
    } catch (err: any) {
      notifyToast(err.message || '保存设置失败', 'error');
    }
  };

  const handleSaveMediaInfo = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await updateMediaInfoSettings(mediaInfo);
      notifyToast('媒体提取与 FFprobe 设置已保存', 'success');
    } catch (err: any) {
      notifyToast(err.message || '保存设置失败', 'error');
    }
  };

  return (
    <div className="space-y-6">
      {/* Users Management Section */}
      <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
              <Users className="w-4 h-4 text-emerald-400" />
              用户与访问权限管理 ({users.length})
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              配置 Emby 客户端访问账号、同时在线设备上限及播放权限
            </p>
          </div>

          <button
            onClick={() => setShowAddUser(true)}
            className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md shadow-emerald-600/30 transition"
          >
            <UserPlus className="w-4 h-4" />
            新增用户
          </button>
        </div>

        <div className="divide-y divide-zinc-800 border border-zinc-800 rounded-xl overflow-hidden bg-zinc-950/40">
          {users.map((u) => {
            const isAdmin = u.Policy?.IsAdministrator ?? false;
            const canPlay = u.Policy?.EnableMediaPlayback ?? true;

            return (
              <div
                key={u.Id}
                className="p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3 text-xs"
              >
                <div className="flex items-center gap-3">
                  <div className="w-8 h-8 rounded-xl bg-zinc-800 border border-zinc-700/80 flex items-center justify-center font-bold text-zinc-200">
                    {u.Name.slice(0, 1).toUpperCase()}
                  </div>
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-semibold text-zinc-200 text-sm">{u.Name}</span>
                      {isAdmin ? (
                        <span className="px-1.5 py-0.2 rounded bg-amber-950 text-amber-400 border border-amber-500/20 text-[10px] font-semibold">
                          管理员
                        </span>
                      ) : (
                        <span className="px-1.5 py-0.2 rounded bg-zinc-800 text-zinc-400 text-[10px]">
                          普通用户
                        </span>
                      )}
                    </div>
                    <div className="text-[11px] text-zinc-500 mt-0.5">
                      设备数上限: {u.MaxDevices || '无限制'} · 播放权限:{' '}
                      <span className={canPlay ? 'text-emerald-400' : 'text-rose-400'}>
                        {canPlay ? '允许' : '已禁止'}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <button
                    onClick={() => {
                      setEditingUser(u);
                      setEditMaxDevices(u.MaxDevices || 5);
                      setEditAllowPlayback(canPlay);
                      setEditPassword('');
                    }}
                    className="p-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 transition"
                    title="编辑用户"
                  >
                    <Edit2 className="w-3.5 h-3.5" />
                  </button>
                  <button
                    onClick={() => handleDeleteUser(u.Id, u.Name)}
                    className="p-1.5 rounded-lg hover:bg-rose-950/60 text-zinc-500 hover:text-rose-400 transition"
                    title="删除用户"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Enhancements & Playback Settings */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
          <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
            <Tv className="w-4 h-4 text-emerald-400" />
            播放与 AList 直链增强
          </h3>
          <form onSubmit={handleSaveEnhancements} className="space-y-3 text-xs">
            <label className="flex items-center justify-between p-3 rounded-xl bg-zinc-950/60 border border-zinc-800 cursor-pointer">
              <div>
                <div className="font-semibold text-zinc-200">启用 AList 302 直链播放</div>
                <div className="text-[11px] text-zinc-500 mt-0.5">
                  优先客户端直连网盘 CDN，不消耗 NAS 任何上行带宽
                </div>
              </div>
              <input
                type="checkbox"
                checked={enhancements.ExternalPlayback ?? true}
                onChange={(e) =>
                  setEnhancements({ ...enhancements, ExternalPlayback: e.target.checked })
                }
                className="w-4 h-4 rounded bg-zinc-900 border-zinc-700 text-emerald-500 focus:ring-0"
              />
            </label>

            <label className="flex items-center justify-between p-3 rounded-xl bg-zinc-950/60 border border-zinc-800 cursor-pointer">
              <div>
                <div className="font-semibold text-zinc-200">智能字幕提取与加载</div>
                <div className="text-[11px] text-zinc-500 mt-0.5">
                  自动探测同名 .srt / .vtt 外挂字幕并加载进播放器
                </div>
              </div>
              <input
                type="checkbox"
                checked={enhancements.AutoExtractSubtitles ?? true}
                onChange={(e) =>
                  setEnhancements({ ...enhancements, AutoExtractSubtitles: e.target.checked })
                }
                className="w-4 h-4 rounded bg-zinc-900 border-zinc-700 text-emerald-500 focus:ring-0"
              />
            </label>

            <div className="pt-2 flex justify-end">
              <button
                type="submit"
                className="px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs shadow-md shadow-emerald-600/20"
              >
                保存增强设置
              </button>
            </div>
          </form>
        </div>

        {/* Media Info & FFprobe Settings */}
        <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
          <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
            <Cpu className="w-4 h-4 text-sky-400" />
            媒体分析与 FFprobe 并发
          </h3>
          <form onSubmit={handleSaveMediaInfo} className="space-y-3 text-xs">
            <div>
              <label className="block text-[11px] font-semibold text-zinc-400 mb-1">
                FFprobe 分析最大并发数
              </label>
              <input
                type="number"
                min={1}
                max={16}
                value={mediaInfo.Concurrency || 2}
                onChange={(e) =>
                  setMediaInfo({ ...mediaInfo, Concurrency: Number(e.target.value) })
                }
                className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 font-mono focus:outline-none focus:border-emerald-500"
              />
              <span className="text-[10px] text-zinc-500 mt-1 block">
                降低 NAS CPU 负载，推荐 2~4
              </span>
            </div>

            <div>
              <label className="block text-[11px] font-semibold text-zinc-400 mb-1">
                网络流媒体探针超时 (秒)
              </label>
              <input
                type="number"
                min={2}
                max={60}
                value={mediaInfo.ProbeTimeoutSeconds || 10}
                onChange={(e) =>
                  setMediaInfo({ ...mediaInfo, ProbeTimeoutSeconds: Number(e.target.value) })
                }
                className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 font-mono focus:outline-none focus:border-emerald-500"
              />
            </div>

            <div className="pt-2 flex justify-end">
              <button
                type="submit"
                className="px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs shadow-md shadow-emerald-600/20"
              >
                保存分析设置
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Add User Modal */}
      {showAddUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-sm bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
              <UserPlus className="w-4 h-4 text-emerald-400" />
              新增用户账号
            </h3>
            <form onSubmit={handleAddUserSubmit} className="space-y-3 text-xs">
              <div>
                <label className="block text-zinc-400 mb-1 font-medium">用户名</label>
                <input
                  type="text"
                  required
                  value={addName}
                  onChange={(e) => setAddName(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 focus:outline-none focus:border-emerald-500"
                  placeholder="例如: family"
                />
              </div>
              <div>
                <label className="block text-zinc-400 mb-1 font-medium">密码 (留空则无密码)</label>
                <input
                  type="password"
                  value={addPassword}
                  onChange={(e) => setAddPassword(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 focus:outline-none focus:border-emerald-500"
                  placeholder="请输入登录密码"
                />
              </div>
              <div>
                <label className="block text-zinc-400 mb-1 font-medium">最大在线设备数</label>
                <input
                  type="number"
                  min={1}
                  value={addMaxDevices}
                  onChange={(e) => setAddMaxDevices(Number(e.target.value))}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 font-mono focus:outline-none focus:border-emerald-500"
                />
              </div>
              <label className="flex items-center gap-2 cursor-pointer pt-1">
                <input
                  type="checkbox"
                  checked={addAllowPlayback}
                  onChange={(e) => setAddAllowPlayback(e.target.checked)}
                  className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-emerald-500 focus:ring-0"
                />
                <span className="text-zinc-200">允许媒体播放</span>
              </label>
              <div className="flex items-center justify-end gap-2 pt-3 border-t border-zinc-800">
                <button
                  type="button"
                  onClick={() => setShowAddUser(false)}
                  className="px-3.5 py-1.5 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-4 py-1.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-semibold"
                >
                  创建
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Edit User Modal */}
      {editingUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-sm bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
              <Edit2 className="w-4 h-4 text-emerald-400" />
              编辑用户: {editingUser.Name}
            </h3>
            <form onSubmit={handleEditUserSubmit} className="space-y-3 text-xs">
              <div>
                <label className="block text-zinc-400 mb-1 font-medium">重设密码 (不改留空)</label>
                <input
                  type="password"
                  value={editPassword}
                  onChange={(e) => setEditPassword(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 focus:outline-none focus:border-emerald-500"
                  placeholder="留空保持原密码"
                />
              </div>
              <div>
                <label className="block text-zinc-400 mb-1 font-medium">最大在线设备数</label>
                <input
                  type="number"
                  min={1}
                  value={editMaxDevices}
                  onChange={(e) => setEditMaxDevices(Number(e.target.value))}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-zinc-200 font-mono focus:outline-none focus:border-emerald-500"
                />
              </div>
              <label className="flex items-center gap-2 cursor-pointer pt-1">
                <input
                  type="checkbox"
                  checked={editAllowPlayback}
                  onChange={(e) => setEditAllowPlayback(e.target.checked)}
                  className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-emerald-500 focus:ring-0"
                />
                <span className="text-zinc-200">允许媒体播放</span>
              </label>
              <div className="flex items-center justify-end gap-2 pt-3 border-t border-zinc-800">
                <button
                  type="button"
                  onClick={() => setEditingUser(null)}
                  className="px-3.5 py-1.5 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-4 py-1.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-semibold"
                >
                  保存
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
