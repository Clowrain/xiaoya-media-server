import React, { useState, useEffect } from 'react';
import { 
  Folder, Plus, RefreshCw, Trash2, Edit2, FolderPlus, 
  Clock, Play, ShieldAlert, Check, X, HardDrive, Image as ImageIcon, Upload 
} from 'lucide-react';
import { AdminLibrary } from '../../types';
import { 
  fetchAdminLibraries, createAdminLibrary, updateAdminLibrary, 
  deleteAdminLibrary, addLibraryFolder, removeLibraryFolder, 
  scanLibrary, scanAllLibraries, fetchScanSchedule, updateScanSchedule, 
  uploadItemCover, deleteItemCover, notifyToast 
} from '../../api/client';
import { DirectoryPickerModal } from '../../components/DirectoryPickerModal';

interface AdminLibrariesProps {
  onOpenLogs: () => void;
}

export const AdminLibraries: React.FC<AdminLibrariesProps> = ({ onOpenLogs }) => {
  const [libraries, setLibraries] = useState<AdminLibrary[]>([]);
  const [loading, setLoading] = useState<boolean>(true);

  // Modals state
  const [showAddModal, setShowAddModal] = useState<boolean>(false);
  const [showEditModal, setShowEditModal] = useState<AdminLibrary | null>(null);
  const [showFoldersModal, setShowFoldersModal] = useState<AdminLibrary | null>(null);
  const [showScheduleModal, setShowScheduleModal] = useState<boolean>(false);
  const [showCoverModal, setShowCoverModal] = useState<AdminLibrary | null>(null);
  const [coverVersion, setCoverVersion] = useState<number>(Date.now());

  // Directory picker state
  const [pickerTarget, setPickerTarget] = useState<'add' | 'folder' | 'edit-folder' | null>(null);
  const [currentSelectedPath, setCurrentSelectedPath] = useState<string>('/media/strm');

  // Form states - Add
  const [addName, setAddName] = useState('');
  const [addKind, setAddKind] = useState('tvshows');
  const [addPaths, setAddPaths] = useState<string[]>(['/media/strm']);
  const [addInputPath, setAddInputPath] = useState('');

  // Form states - Edit
  const [editName, setEditName] = useState('');
  const [editKind, setEditKind] = useState('movies');
  const [editPaths, setEditPaths] = useState<string[]>([]);
  const [editNewPath, setEditNewPath] = useState<string>('');

  // Form states - Manage folders
  const [newFolderPath, setNewFolderPath] = useState('');

  // Schedule state
  const [scheduleEnabled, setScheduleEnabled] = useState(false);
  const [scheduleFreq, setScheduleFreq] = useState('Daily');
  const [scheduleTime, setScheduleTime] = useState('03:00');
  const [scheduleMinutes, setScheduleMinutes] = useState(60);

  // Helper to extract full paths list with fallbacks
  const getLibPaths = (lib?: AdminLibrary | null): string[] => {
    if (!lib) return [];
    if (Array.isArray(lib.Paths) && lib.Paths.length > 0) return lib.Paths;
    if (Array.isArray(lib.Locations) && lib.Locations.length > 0) return lib.Locations;
    if (lib.Path) return [lib.Path];
    return [];
  };

  const getKindBadge = (kind?: string, collectionType?: string) => {
    const k = (kind || collectionType || '').toLowerCase();
    if (k === 'tvshows' || k === 'tv' || k === 'series') {
      return { text: '剧集', className: 'text-indigo-400 bg-indigo-950/60 border-indigo-500/20' };
    }
    if (k === 'music') {
      return { text: '音乐', className: 'text-amber-400 bg-amber-950/60 border-amber-500/20' };
    }
    return { text: '电影', className: 'text-emerald-400 bg-emerald-950/60 border-emerald-500/20' };
  };

  const formatScannedTime = (scanned?: number) => {
    if (!scanned || scanned <= 0) return null;
    const d = new Date(scanned * 1000);
    return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
  };

  const loadLibraries = async (showSpinner = true) => {
    if (showSpinner) setLoading(true);
    try {
      const data = await fetchAdminLibraries();
      setLibraries(data);
    } catch (err: any) {
      if (showSpinner) {
        notifyToast(err.message || '加载媒体库列表失败', 'error');
      }
    } finally {
      if (showSpinner) setLoading(false);
    }
  };

  useEffect(() => {
    loadLibraries(true);
  }, []);

  // Poll library status if any library is scanning
  useEffect(() => {
    const hasActiveScan = libraries.some(l => l.Status === 'scanning');
    if (!hasActiveScan) return;
    const timer = setInterval(() => {
      loadLibraries(false);
    }, 2500);
    return () => clearInterval(timer);
  }, [libraries]);

  const handleScan = async (id: string, mode: 'update' | 'refresh') => {
    try {
      setLibraries(prev => prev.map(l => l.Id === id ? { ...l, Status: 'scanning', Error: '' } : l));
      await scanLibrary(id, mode);
      notifyToast(mode === 'update' ? '增量扫描任务已加入队列' : '全量刷新任务已加入队列', 'success');
      onOpenLogs();
    } catch (err: any) {
      notifyToast(err.message || '触发扫描失败', 'error');
      loadLibraries(false);
    }
  };

  const handleScanAll = async () => {
    try {
      setLibraries(prev => prev.map(l => ({ ...l, Status: 'scanning', Error: '' })));
      await scanAllLibraries();
      notifyToast('所有媒体库扫描已加入队列', 'success');
      onOpenLogs();
    } catch (err: any) {
      notifyToast(err.message || '触发全库扫描失败', 'error');
      loadLibraries(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`确定删除媒体库「${name}」？底层存储的物理/STRM文件不会被删除。`)) return;
    try {
      await deleteAdminLibrary(id);
      notifyToast(`媒体库「${name}」已删除`, 'success');
      loadLibraries();
    } catch (err: any) {
      notifyToast(err.message || '删除媒体库失败', 'error');
    }
  };

  const handleAddCreatePath = (pathToAdd?: string) => {
    const p = (pathToAdd ?? addInputPath).trim();
    if (!p) return;
    if (addPaths.includes(p)) {
      notifyToast('该路径已在列表中', 'warning');
      return;
    }
    setAddPaths(prev => [...prev, p]);
    if (!pathToAdd) setAddInputPath('');
  };

  const handleRemoveCreatePath = (pathToRemove: string) => {
    if (addPaths.length <= 1) {
      notifyToast('媒体库至少需要保留一个目录', 'warning');
      return;
    }
    setAddPaths(prev => prev.filter(p => p !== pathToRemove));
  };

  const handleAddSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    let finalPaths = [...addPaths];
    if (addInputPath.trim() && !finalPaths.includes(addInputPath.trim())) {
      finalPaths.push(addInputPath.trim());
    }
    if (!addName.trim() || finalPaths.length === 0) {
      notifyToast('媒体库名称与至少一个目录路径不能为空', 'error');
      return;
    }
    try {
      await createAdminLibrary(addName.trim(), addKind, finalPaths);
      notifyToast(`成功创建媒体库「${addName}」`, 'success');
      setShowAddModal(false);
      setAddName('');
      setAddPaths(['/media/strm']);
      setAddInputPath('');
      loadLibraries();
    } catch (err: any) {
      notifyToast(err.message || '创建媒体库失败', 'error');
    }
  };

  const openEditModal = (lib: AdminLibrary) => {
    setShowEditModal(lib);
    setEditName(lib.Name);
    setEditKind(lib.Kind || lib.CollectionType || 'movies');
    setEditPaths(getLibPaths(lib));
    setEditNewPath('');
  };

  const handleAddEditPath = () => {
    const p = editNewPath.trim();
    if (!p) return;
    if (editPaths.includes(p)) {
      notifyToast('该路径已在列表中', 'warning');
      return;
    }
    setEditPaths([...editPaths, p]);
    setEditNewPath('');
  };

  const handleRemoveEditPath = (pathToRemove: string) => {
    if (editPaths.length <= 1) {
      notifyToast('媒体库至少需要保留一个目录', 'warning');
      return;
    }
    setEditPaths(editPaths.filter(p => p !== pathToRemove));
  };

  const handleEditSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!showEditModal || !editName.trim()) return;
    if (editPaths.length === 0) {
      notifyToast('媒体库至少需要包含一个有效路径', 'error');
      return;
    }
    try {
      await updateAdminLibrary(showEditModal.Id, editName.trim(), editKind, editPaths);
      notifyToast('媒体库配置已更新', 'success');
      setShowEditModal(null);
      loadLibraries(false);
    } catch (err: any) {
      notifyToast(err.message || '更新媒体库失败', 'error');
    }
  };

  const handleAddFolder = async () => {
    if (!showFoldersModal || !newFolderPath.trim()) return;
    try {
      await addLibraryFolder(showFoldersModal.Id, newFolderPath.trim());
      notifyToast('目录已添加到媒体库', 'success');
      setNewFolderPath('');
      const updated = await fetchAdminLibraries();
      setLibraries(updated);
      const found = updated.find((l) => l.Id === showFoldersModal.Id);
      if (found) setShowFoldersModal(found);
    } catch (err: any) {
      notifyToast(err.message || '添加目录失败', 'error');
    }
  };

  const handleRemoveFolder = async (path: string) => {
    if (!showFoldersModal) return;
    const currentPaths = getLibPaths(showFoldersModal);
    if (currentPaths.length <= 1) {
      notifyToast('媒体库至少需要保留一个目录', 'warning');
      return;
    }
    if (!confirm(`确定解绑目录 ${path}？`)) return;
    try {
      await removeLibraryFolder(showFoldersModal.Id, path);
      notifyToast('已解绑目录', 'success');
      const updated = await fetchAdminLibraries();
      setLibraries(updated);
      const found = updated.find((l) => l.Id === showFoldersModal.Id);
      if (found) setShowFoldersModal(found);
    } catch (err: any) {
      notifyToast(err.message || '移除目录失败', 'error');
    }
  };

  const openScheduleModal = async () => {
    try {
      const s = await fetchScanSchedule();
      setScheduleEnabled(s.Enabled ?? false);
      setScheduleFreq(s.Frequency || 'Daily');
      setScheduleTime(s.Time || '03:00');
      setScheduleMinutes(s.Minutes || 60);
      setShowScheduleModal(true);
    } catch (err: any) {
      notifyToast(err.message || '读取扫描计划失败', 'error');
    }
  };

  const handleSaveSchedule = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await updateScanSchedule({
        Enabled: scheduleEnabled,
        Frequency: scheduleFreq,
        Time: scheduleTime,
        Minutes: scheduleMinutes,
      });
      notifyToast('扫描计划设置已保存', 'success');
      setShowScheduleModal(false);
    } catch (err: any) {
      notifyToast(err.message || '保存计划失败', 'error');
    }
  };

  return (
    <div className="space-y-6">
      {/* Top Action Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 bg-zinc-900/60 border border-zinc-800 p-4 rounded-2xl">
        <div>
          <h2 className="text-base font-bold text-zinc-100 flex items-center gap-2">
            <Folder className="w-5 h-5 text-emerald-400" />
            媒体库管理
          </h2>
          <p className="text-xs text-zinc-400 mt-0.5">
            管理媒体库分类、多文件夹挂载路径、触发扫描入库与配置定时计划
          </p>
        </div>

        <div className="flex items-center gap-2.5">
          <button
            onClick={openScheduleModal}
            className="flex items-center gap-1.5 px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-300 text-xs font-medium transition"
          >
            <Clock className="w-3.5 h-3.5 text-sky-400" />
            定时扫描设置
          </button>

          <button
            onClick={handleScanAll}
            className="flex items-center gap-1.5 px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-300 text-xs font-medium transition"
          >
            <RefreshCw className="w-3.5 h-3.5 text-emerald-400" />
            全库扫描
          </button>

          <button
            onClick={() => setShowAddModal(true)}
            className="flex items-center gap-1.5 px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-lg shadow-emerald-600/30 transition"
          >
            <Plus className="w-4 h-4" />
            新建媒体库
          </button>
        </div>
      </div>

      {/* Library Cards */}
      {loading ? (
        <div className="py-20 text-center text-zinc-500">正在加载媒体库...</div>
      ) : libraries.length === 0 ? (
        <div className="py-20 text-center text-zinc-500 bg-zinc-900/30 border border-zinc-800 rounded-2xl p-8">
          <FolderPlus className="w-12 h-12 mx-auto mb-3 opacity-30 text-zinc-400" />
          <p className="font-semibold text-zinc-300">暂无媒体库</p>
          <p className="text-xs text-zinc-500 mt-1">点击右上角「新建媒体库」添加电影或电视剧目录</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {libraries.map((lib) => {
            const paths = getLibPaths(lib);
            const badge = getKindBadge(lib.Kind, lib.CollectionType);
            const isScanning = lib.Status === 'scanning';
            const itemCount = lib.Items ?? lib.Count ?? 0;
            const lastScan = formatScannedTime(lib.Scanned);

            return (
              <div
                key={lib.Id}
                className="p-5 rounded-2xl bg-zinc-900/80 border border-zinc-800 hover:border-zinc-700 transition flex flex-col justify-between space-y-4"
              >
                <div>
                  <div className="flex items-start justify-between">
                    <div className="flex items-start gap-3">
                      {/* 媒体库封面缩略图预览 */}
                      <div 
                        onClick={() => setShowCoverModal(lib)}
                        className="group relative w-12 h-16 rounded-xl overflow-hidden bg-zinc-950 border border-zinc-800 shrink-0 cursor-pointer shadow-sm hover:border-emerald-500/60 transition"
                        title="点击更换媒体库封面"
                      >
                        <img 
                          src={`/Items/${lib.Id}/Images/Primary?fillHeight=160&v=${coverVersion}`}
                          alt={lib.Name}
                          className="w-full h-full object-cover transition duration-300 group-hover:scale-110"
                        />
                        <div className="absolute inset-0 bg-black/60 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                          <ImageIcon className="w-4 h-4 text-emerald-400" />
                        </div>
                      </div>

                      <div>
                        <div className="flex items-center gap-2">
                          <h3 className="font-bold text-sm text-zinc-100">{lib.Name}</h3>
                          <span className={`text-[10px] px-1.5 py-0.5 rounded border font-medium ${badge.className}`}>
                            {badge.text}
                          </span>
                        </div>
                        <div className="flex items-center gap-2 mt-0.5 text-xs text-zinc-400">
                          <span>{itemCount} 部媒体</span>
                          {isScanning ? (
                            <span className="text-[11px] text-amber-400 font-medium animate-pulse">正在扫描中...</span>
                          ) : lib.Error ? (
                            <span className="text-[11px] text-rose-400 font-medium truncate max-w-[140px]" title={lib.Error}>
                              异常: {lib.Error}
                            </span>
                          ) : lastScan ? (
                            <span className="text-[10px] text-zinc-500">最近扫描: {lastScan}</span>
                          ) : null}
                        </div>
                      </div>
                    </div>

                    <div className="flex items-center gap-1">
                      <button
                        onClick={() => setShowCoverModal(lib)}
                        className="p-1.5 rounded-lg hover:bg-zinc-800 text-zinc-400 hover:text-emerald-400 transition"
                        title="设置封面海报"
                      >
                        <ImageIcon className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => openEditModal(lib)}
                        className="p-1.5 rounded-lg hover:bg-zinc-800 text-zinc-400 hover:text-zinc-200 transition"
                        title="编辑基本信息与路径"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => handleDelete(lib.Id, lib.Name)}
                        className="p-1.5 rounded-lg hover:bg-rose-950/50 text-zinc-400 hover:text-rose-400 transition"
                        title="删除媒体库"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>

                  {/* Bound Paths */}
                  <div className="mt-4 space-y-1.5">
                    <div className="text-[11px] font-semibold text-zinc-500 flex items-center justify-between">
                      <span>关联存储目录 ({paths.length}):</span>
                      <button
                        onClick={() => setShowFoldersModal(lib)}
                        className="text-emerald-400 hover:underline text-[11px]"
                      >
                        管理路径
                      </button>
                    </div>
                    {paths.length === 0 ? (
                      <div className="text-xs text-zinc-500 italic px-2.5 py-1.5 rounded-lg bg-zinc-950 border border-zinc-800/80">
                        暂无关联路径，请点击「管理路径」添加
                      </div>
                    ) : (
                      paths.slice(0, 2).map((p, idx) => (
                        <div
                          key={idx}
                          className="text-xs font-mono px-2.5 py-1.5 rounded-lg bg-zinc-950 border border-zinc-800/80 text-zinc-300 truncate"
                          title={p}
                        >
                          {p}
                        </div>
                      ))
                    )}
                    {paths.length > 2 && (
                      <div className="text-[10px] text-zinc-500 pl-1">
                        还有 {paths.length - 2} 个路径...
                      </div>
                    )}
                  </div>
                </div>

                {/* Action Buttons */}
                <div className="pt-2 border-t border-zinc-800/60 flex items-center gap-2">
                  <button
                    onClick={() => handleScan(lib.Id, 'update')}
                    disabled={isScanning}
                    className="flex-1 flex items-center justify-center gap-1.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 disabled:opacity-50 text-zinc-200 text-xs font-medium transition"
                  >
                    <RefreshCw className={`w-3.5 h-3.5 text-emerald-400 ${isScanning ? 'animate-spin' : ''}`} />
                    {isScanning ? '扫描中...' : '增量扫描'}
                  </button>
                  <button
                    onClick={() => handleScan(lib.Id, 'refresh')}
                    disabled={isScanning}
                    className="flex items-center justify-center gap-1.5 px-3 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 disabled:opacity-50 text-zinc-300 text-xs font-medium transition"
                    title="全量扫描并刷新元数据"
                  >
                    全量刷新
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Add Library Modal */}
      {showAddModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-lg bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-5">
            <div className="flex items-center justify-between border-b border-zinc-800/80 pb-4">
              <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
                <FolderPlus className="w-5 h-5 text-emerald-400" />
                新建媒体库
              </h3>
              <button
                onClick={() => setShowAddModal(false)}
                className="p-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-400 hover:text-white"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <form onSubmit={handleAddSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">媒体库名称</label>
                <input
                  type="text"
                  required
                  value={addName}
                  onChange={(e) => setAddName(e.target.value)}
                  placeholder="例如: 电影、华语剧集、欧美精选"
                  className="w-full px-3.5 py-2.5 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                />
              </div>

              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">内容类型</label>
                <select
                  value={addKind}
                  onChange={(e) => setAddKind(e.target.value)}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                >
                  <option value="movies">电影 (Movies)</option>
                  <option value="tvshows">剧集/动漫/纪录片 (TV Shows)</option>
                  <option value="music">音乐 (Music)</option>
                  <option value="others">其它混合 (Mixed)</option>
                </select>
              </div>

              {/* Multiple Associated Paths */}
              <div className="space-y-2 pt-1 border-t border-zinc-800/60">
                <label className="block text-xs font-semibold text-zinc-400">
                  关联存储路径 ({addPaths.length})
                </label>
                <div className="space-y-1.5 max-h-40 overflow-y-auto pr-1">
                  {addPaths.map((p) => (
                    <div
                      key={p}
                      className="flex items-center justify-between p-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono"
                    >
                      <span className="text-zinc-200 truncate mr-2" title={p}>{p}</span>
                      <button
                        type="button"
                        onClick={() => handleRemoveCreatePath(p)}
                        className="p-1 rounded text-zinc-500 hover:text-rose-400 transition shrink-0"
                        title="从列表中移除"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ))}
                </div>

                {/* Add path input */}
                <div className="flex items-center gap-2 pt-1">
                  <input
                    type="text"
                    value={addInputPath}
                    onChange={(e) => setAddInputPath(e.target.value)}
                    placeholder="/media/strm 或其它已挂载目录"
                    className="flex-1 px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-200 focus:outline-none focus:border-emerald-500"
                  />
                  <button
                    type="button"
                    onClick={() => {
                      setCurrentSelectedPath(addInputPath || '/media/strm');
                      setPickerTarget('add');
                    }}
                    className="px-3 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium shrink-0"
                  >
                    浏览
                  </button>
                  <button
                    type="button"
                    onClick={() => handleAddCreatePath()}
                    className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-emerald-400 text-xs font-medium shrink-0"
                  >
                    添加
                  </button>
                </div>

                {/* Quick Shortcuts */}
                <div className="flex items-center gap-1.5 mt-2 flex-wrap text-[11px]">
                  <span className="text-zinc-500">快捷添加:</span>
                  {['/media/strm', '/media/strm/电影', '/media/strm/电视剧'].map((p) => (
                    <button
                      key={p}
                      type="button"
                      onClick={() => handleAddCreatePath(p)}
                      className="px-2 py-0.5 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-300 font-mono"
                    >
                      +{p}
                    </button>
                  ))}
                </div>
              </div>

              <div className="flex items-center justify-end gap-3 pt-3 border-t border-zinc-800/80">
                <button
                  type="button"
                  onClick={() => setShowAddModal(false)}
                  className="px-4 py-2.5 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800 text-xs transition"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-5 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-lg shadow-emerald-600/30 transition"
                >
                  确认创建
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Edit Library Modal */}
      {showEditModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-lg bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-zinc-800/80 pb-3">
              <div>
                <h3 className="text-base font-bold text-zinc-100">编辑媒体库</h3>
                <p className="text-xs text-zinc-400 mt-0.5">修改媒体库名称、分类及关联存储路径</p>
              </div>
              <button
                onClick={() => setShowEditModal(null)}
                className="p-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-400 hover:text-white"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <form onSubmit={handleEditSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">媒体库名称</label>
                <input
                  type="text"
                  required
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                />
              </div>

              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">内容类型</label>
                <select
                  value={editKind}
                  onChange={(e) => setEditKind(e.target.value)}
                  className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                >
                  <option value="movies">电影 (Movies)</option>
                  <option value="tvshows">剧集/电视剧 (TV Shows)</option>
                  <option value="music">音乐 (Music)</option>
                  <option value="others">其它混合 (Mixed)</option>
                </select>
              </div>

              {/* Edit Associated Paths */}
              <div className="space-y-2 pt-1 border-t border-zinc-800/60">
                <label className="block text-xs font-semibold text-zinc-400">
                  关联存储路径 ({editPaths.length})
                </label>
                <div className="space-y-1.5 max-h-40 overflow-y-auto pr-1">
                  {editPaths.map((p) => (
                    <div
                      key={p}
                      className="flex items-center justify-between p-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono"
                    >
                      <span className="text-zinc-200 truncate mr-2" title={p}>{p}</span>
                      <button
                        type="button"
                        onClick={() => handleRemoveEditPath(p)}
                        className="p-1 rounded text-zinc-500 hover:text-rose-400 transition shrink-0"
                        title="从列表中移除"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ))}
                </div>

                {/* Add path input */}
                <div className="flex items-center gap-2 pt-1">
                  <input
                    type="text"
                    value={editNewPath}
                    onChange={(e) => setEditNewPath(e.target.value)}
                    placeholder="/media/strm/新目录"
                    className="flex-1 px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-200 focus:outline-none focus:border-emerald-500"
                  />
                  <button
                    type="button"
                    onClick={() => {
                      setCurrentSelectedPath(editNewPath || '/media/strm');
                      setPickerTarget('edit-folder');
                    }}
                    className="px-3 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium shrink-0"
                  >
                    浏览
                  </button>
                  <button
                    type="button"
                    onClick={handleAddEditPath}
                    className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-emerald-400 text-xs font-medium shrink-0"
                  >
                    添加
                  </button>
                </div>
              </div>

              <div className="flex items-center justify-end gap-3 pt-3 border-t border-zinc-800/80">
                <button
                  type="button"
                  onClick={() => setShowEditModal(null)}
                  className="px-4 py-2 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800 text-xs transition"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-5 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-lg shadow-emerald-600/30 transition"
                >
                  保存修改
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Folders Management Modal */}
      {showFoldersModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-lg bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-zinc-800/80 pb-3">
              <div>
                <h3 className="text-base font-bold text-zinc-100">管理媒体库关联路径</h3>
                <p className="text-xs text-zinc-400">{showFoldersModal.Name}</p>
              </div>
              <button
                onClick={() => setShowFoldersModal(null)}
                className="p-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-400 hover:text-white"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            {/* Existing Paths */}
            <div className="space-y-2 max-h-48 overflow-y-auto">
              {getLibPaths(showFoldersModal).map((p) => (
                <div
                  key={p}
                  className="flex items-center justify-between p-2.5 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono"
                >
                  <span className="text-zinc-200 truncate mr-2" title={p}>{p}</span>
                  <button
                    onClick={() => handleRemoveFolder(p)}
                    className="p-1 rounded text-zinc-500 hover:text-rose-400 transition shrink-0"
                    title="移除此目录"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              ))}
            </div>

            {/* Add new folder */}
            <div className="pt-2 border-t border-zinc-800/80 space-y-2">
              <label className="block text-xs font-semibold text-zinc-400">添加新目录</label>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={newFolderPath}
                  onChange={(e) => setNewFolderPath(e.target.value)}
                  placeholder="/media/strm/其他目录"
                  className="flex-1 px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-200 focus:outline-none focus:border-emerald-500"
                />
                <button
                  type="button"
                  onClick={() => {
                    setCurrentSelectedPath(newFolderPath || '/media/strm');
                    setPickerTarget('folder');
                  }}
                  className="px-3 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium shrink-0"
                >
                  浏览
                </button>
                <button
                  type="button"
                  onClick={handleAddFolder}
                  className="px-3.5 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shrink-0"
                >
                  添加
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Scheduled Scan Modal */}
      {showScheduleModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-md bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-zinc-800/80 pb-3">
              <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
                <Clock className="w-4 h-4 text-sky-400" />
                自动定时扫描计划
              </h3>
              <button
                onClick={() => setShowScheduleModal(false)}
                className="p-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-400 hover:text-white"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <form onSubmit={handleSaveSchedule} className="space-y-4">
              <label className="flex items-center gap-2.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={scheduleEnabled}
                  onChange={(e) => setScheduleEnabled(e.target.checked)}
                  className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-emerald-500 focus:ring-0"
                />
                <span className="text-sm font-medium text-zinc-200">启用定时自动扫描</span>
              </label>

              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">频率模式</label>
                <select
                  value={scheduleFreq}
                  onChange={(e) => setScheduleFreq(e.target.value)}
                  className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                >
                  <option value="Daily">每天固定时间 (Daily)</option>
                  <option value="Interval">按分钟循环间隔 (Interval)</option>
                </select>
              </div>

              {scheduleFreq === 'Daily' ? (
                <div>
                  <label className="block text-xs font-semibold text-zinc-400 mb-1.5">每天执行时间</label>
                  <input
                    type="time"
                    value={scheduleTime}
                    onChange={(e) => setScheduleTime(e.target.value)}
                    className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                  />
                </div>
              ) : (
                <div>
                  <label className="block text-xs font-semibold text-zinc-400 mb-1.5">间隔分钟数</label>
                  <input
                    type="number"
                    min="5"
                    max="1440"
                    value={scheduleMinutes}
                    onChange={(e) => setScheduleMinutes(parseInt(e.target.value) || 60)}
                    className="w-full px-3 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500"
                  />
                </div>
              )}

              <div className="flex items-center justify-end gap-3 pt-3 border-t border-zinc-800/80">
                <button
                  type="button"
                  onClick={() => setShowScheduleModal(false)}
                  className="px-4 py-2 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800 text-xs transition"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-5 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-lg shadow-emerald-600/30 transition"
                >
                  保存设置
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Manage Cover Modal */}
      {showCoverModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm animate-in fade-in duration-200">
          <div className="relative w-full max-w-md rounded-2xl bg-zinc-900 border border-zinc-800 p-6 shadow-2xl space-y-5">
            <div className="flex items-center justify-between pb-3 border-b border-zinc-800">
              <div className="flex items-center gap-2 text-zinc-100 font-bold text-base">
                <ImageIcon className="w-5 h-5 text-emerald-400" />
                媒体库封面 - {showCoverModal.Name}
              </div>
              <button 
                onClick={() => setShowCoverModal(null)}
                className="p-1 rounded-lg hover:bg-zinc-800 text-zinc-400 hover:text-white transition"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* 当前封面预览 */}
            <div className="flex flex-col items-center justify-center p-4 bg-zinc-950/60 rounded-xl border border-zinc-800/80 space-y-3">
              <div className="w-32 h-48 rounded-xl overflow-hidden bg-zinc-900 border border-zinc-700/60 shadow-xl">
                <img 
                  src={`/Items/${showCoverModal.Id}/Images/Primary?fillHeight=300&v=${coverVersion}`}
                  alt={showCoverModal.Name}
                  className="w-full h-full object-cover"
                />
              </div>
              <span className="text-xs text-zinc-400 font-medium">前台实时展示海报效果</span>
            </div>

            {/* 操作区域 */}
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <label className="flex-1 flex items-center justify-center gap-2 py-2.5 px-4 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-semibold text-xs transition cursor-pointer shadow-md shadow-emerald-600/20">
                  <Upload className="w-4 h-4" />
                  上传自定义海报
                  <input 
                    type="file" 
                    accept="image/jpeg,image/png,image/webp" 
                    className="hidden"
                    onChange={async (e) => {
                      const file = e.target.files?.[0];
                      if (!file) return;
                      if (file.size > 5 * 1024 * 1024) {
                        notifyToast('封面大小不能超过 5MB', 'warning');
                        return;
                      }
                      try {
                        await uploadItemCover(showCoverModal.Id, file);
                        notifyToast('媒体库封面上传成功', 'success');
                        setCoverVersion(Date.now());
                        loadLibraries(false);
                      } catch (err: any) {
                        notifyToast(err.message || '上传封面失败', 'error');
                      }
                    }}
                  />
                </label>

                <button
                  type="button"
                  onClick={async () => {
                    try {
                      await deleteItemCover(showCoverModal.Id);
                      notifyToast('已恢复默认海报（四宫格拼贴/物理探测）', 'success');
                      setCoverVersion(Date.now());
                      loadLibraries(false);
                    } catch (err: any) {
                      notifyToast(err.message || '恢复默认海报失败', 'error');
                    }
                  }}
                  className="py-2.5 px-3.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-300 hover:text-white text-xs font-medium transition cursor-pointer"
                  title="清除自定义封面，恢复为目录物理海报或 2x2 拼贴海报"
                >
                  恢复默认
                </button>
              </div>

              {/* 优先级说明 */}
              <div className="p-3 rounded-xl bg-zinc-800/40 border border-zinc-800 text-[11px] text-zinc-400 leading-relaxed space-y-1">
                <div className="font-semibold text-zinc-300">海报获取优先级：</div>
                <div>1. 物理目录：存储根目录下的 <code className="text-emerald-400">poster.jpg</code> 或 <code className="text-emerald-400">folder.jpg</code></div>
                <div>2. 自定义上传：此处上传的专属海报</div>
                <div>3. 自动生成：提取库内影片合成 <code className="text-sky-400">2×2 电影墙海报</code></div>
                <div>4. 分类兜底：电影/剧集专属艺术主题海报</div>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Directory Picker Modal */}
      {pickerTarget && (
        <DirectoryPickerModal
          isOpen={!!pickerTarget}
          initialPath={currentSelectedPath}
          onSelect={(selected) => {
            if (pickerTarget === 'add') {
              const selectedPath = selected.trim();
              if (selectedPath) {
                if (!addPaths.includes(selectedPath)) {
                  setAddPaths((prev) => [...prev, selectedPath]);
                }
                setAddInputPath('');
              }
            }
            if (pickerTarget === 'folder') setNewFolderPath(selected);
            if (pickerTarget === 'edit-folder') setEditNewPath(selected);
          }}
          onClose={() => setPickerTarget(null)}
        />
      )}
    </div>
  );
};
