import React, { useState, useEffect } from 'react';
import { 
  FolderSync, Database, HardDrive, RefreshCw, Plus, 
  Trash2, Play, ChevronRight, Folder, FileVideo, 
  Search, ArrowLeft, ArrowRight, CheckCircle2, BookmarkPlus 
} from 'lucide-react';
import { SystemOverview, MonitorRoot, AListFileItem } from '../../types';
import { 
  fetchSystemOverview, fetchMonitorRoots, addMonitorRoot, 
  updateMonitorRoot, deleteMonitorRoot, triggerSync, 
  importResource, browseAList, searchAList, notifyToast 
} from '../../api/client';

interface AdminXiaoyaSyncProps {
  onOpenLogs: () => void;
}

export const AdminXiaoyaSync: React.FC<AdminXiaoyaSyncProps> = ({ onOpenLogs }) => {
  // System Overview State
  const [overview, setOverview] = useState<SystemOverview | null>(null);
  const [roots, setRoots] = useState<MonitorRoot[]>([]);
  const [loadingOverview, setLoadingOverview] = useState<boolean>(true);

  // Add Monitor Dialog State
  const [showAddRoot, setShowAddRoot] = useState<boolean>(false);
  const [newRootPath, setNewRootPath] = useState<string>('');
  const [newInterval, setNewInterval] = useState<number>(3600);
  const [newEnabled, setNewEnabled] = useState<boolean>(true);

  // AList File Explorer State
  const [alistPath, setAlistPath] = useState<string>('/');
  const [alistItems, setAlistItems] = useState<AListFileItem[]>([]);
  const [alistTotal, setAlistTotal] = useState<number>(0);
  const [alistPage, setAlistPage] = useState<number>(1);
  const [alistPerPage] = useState<number>(25);
  const [searchInput, setSearchInput] = useState<string>('');
  const [activeSearch, setActiveSearch] = useState<string>('');
  const [alistLoading, setAlistLoading] = useState<boolean>(false);

  // Load Overview & Roots
  const loadSystemInfo = async () => {
    setLoadingOverview(true);
    try {
      const [ov, r] = await Promise.all([
        fetchSystemOverview(),
        fetchMonitorRoots(),
      ]);
      setOverview(ov);
      setRoots(r);
    } catch (err: any) {
      notifyToast(err.message || '加载系统概览失败', 'error');
    } finally {
      setLoadingOverview(false);
    }
  };

  // Browse AList Directory
  const loadDirectory = async (targetPath: string = alistPath, page: number = 1) => {
    setAlistLoading(true);
    try {
      const res = await browseAList(targetPath, page, alistPerPage);
      const items = res.items || [];
      setAlistItems(items);
      const total = typeof res.total === 'number' && res.total > 0 ? res.total : items.length;
      setAlistTotal(total);
      setAlistPath(res.path || targetPath);
      setAlistPage(page);
    } catch (err: any) {
      notifyToast(err.message || '读取 AList 目录失败', 'error');
    } finally {
      setAlistLoading(false);
    }
  };

  // Execute AList Search
  const executeSearch = async (kw: string, targetPath: string = alistPath, page: number = 1) => {
    setAlistLoading(true);
    try {
      const res = await searchAList(kw, targetPath, page, alistPerPage);
      const items = res.items || [];
      setAlistItems(items);
      const total = typeof res.total === 'number' && res.total > 0 ? res.total : items.length;
      setAlistTotal(total);
      setAlistPage(page);
    } catch (err: any) {
      notifyToast(err.message || '搜索 AList 资源失败', 'error');
    } finally {
      setAlistLoading(false);
    }
  };

  // Navigate to a directory (always clears active search)
  const navigateTo = (targetPath: string) => {
    setActiveSearch('');
    setSearchInput('');
    loadDirectory(targetPath, 1);
  };

  useEffect(() => {
    loadSystemInfo();
    loadDirectory('/', 1);
  }, []);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const kw = searchInput.trim();
    if (!kw) {
      notifyToast('请输入搜索关键词', 'info');
      return;
    }
    setActiveSearch(kw);
    executeSearch(kw, alistPath, 1);
  };

  const handleClearSearch = () => {
    setSearchInput('');
    setActiveSearch('');
    loadDirectory(alistPath, 1);
  };

  const handlePageChange = (newPage: number) => {
    if (newPage < 1 || newPage > totalPages || alistLoading) return;
    if (activeSearch.trim()) {
      executeSearch(activeSearch.trim(), alistPath, newPage);
    } else {
      loadDirectory(alistPath, newPage);
    }
  };

  // Monitor Root Actions
  const handleToggleRoot = async (id: string, currentEnabled: boolean, interval: number) => {
    try {
      await updateMonitorRoot(id, !currentEnabled, interval);
      notifyToast(!currentEnabled ? '已启用监控' : '已停用监控', 'success');
      loadSystemInfo();
    } catch (err: any) {
      notifyToast(err.message || '切换状态失败', 'error');
    }
  };

  const handleDeleteRoot = async (id: string, path: string) => {
    if (!confirm(`确定移除监控 ${path}？已生成的本地 STRM 文件将受到 keep 保护继续保留。`)) return;
    try {
      await deleteMonitorRoot(id);
      notifyToast('已移除监控目录', 'success');
      loadSystemInfo();
    } catch (err: any) {
      notifyToast(err.message || '移除监控失败', 'error');
    }
  };

  const handleTriggerSync = async (id: string, full: boolean) => {
    notifyToast(full ? '全量同步已触发…' : '增量同步已触发…', 'info');
    try {
      const res = await triggerSync(id, full);
      notifyToast(`同步完成：处理 ${(res.batches || []).length} 个批次`, 'success');
      loadSystemInfo();
    } catch (err: any) {
      notifyToast(err.message || '触发同步失败', 'error');
    }
  };

  const handleAddRootSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newRootPath.trim()) return;
    try {
      await addMonitorRoot(newRootPath.trim(), newInterval, newEnabled);
      notifyToast('监控目录已成功添加', 'success');
      setShowAddRoot(false);
      setNewRootPath('');
      loadSystemInfo();
    } catch (err: any) {
      notifyToast(err.message || '添加监控目录失败', 'error');
    }
  };

  // AList Import Resource
  const handleImport = async (item: AListFileItem, track: boolean) => {
    const itemPath = item.full_path || (item.parent ? `${item.parent.replace(/\/$/, '')}/${item.name}` : (alistPath === '/' ? `/${item.name}` : `${alistPath}/${item.name}`));
    notifyToast(track ? `正在整剧追更入库「${item.name}」…` : `正在单次入库「${item.name}」…`, 'info');
    try {
      const res = await importResource(itemPath, item.is_dir, track);
      notifyToast(`导入成功：新增/变更 ${res.created_or_updated || 1} 个资源`, 'success');
      loadSystemInfo();
    } catch (err: any) {
      notifyToast(err.message || '导入资源失败', 'error');
    }
  };

  const totalPages = Math.max(1, Math.ceil(alistTotal / alistPerPage));

  // Breadcrumb items
  const breadcrumbs = (alistPath || '/').split('/').filter(Boolean);

  const strmCount = overview?.strm_count ?? overview?.resources?.active_files ?? 0;
  const cacheSizeBytes = overview?.cache_db_size_bytes ?? 0;
  const cacheSizeMb = cacheSizeBytes > 0 ? (cacheSizeBytes / 1024 / 1024).toFixed(2) : '0.00';
  const alistHost = overview?.alist_host || overview?.alist_base_url || '—';
  const rootsList = Array.isArray(roots) ? roots : [];
  const alistList = Array.isArray(alistItems) ? alistItems : [];

  return (
    <div className="space-y-6">
      {/* System Overview Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="p-4 rounded-2xl bg-zinc-900/80 border border-zinc-800 flex items-center gap-3">
          <div className="w-10 h-10 rounded-xl bg-emerald-950 border border-emerald-500/20 flex items-center justify-center text-emerald-400">
            <HardDrive className="w-5 h-5" />
          </div>
          <div>
            <div className="text-xs text-zinc-400">STRM 文件总数</div>
            <div className="text-xl font-bold font-mono text-zinc-100">
              {strmCount.toLocaleString()}
            </div>
          </div>
        </div>

        <div className="p-4 rounded-2xl bg-zinc-900/80 border border-zinc-800 flex items-center gap-3">
          <div className="w-10 h-10 rounded-xl bg-sky-950 border border-sky-500/20 flex items-center justify-center text-sky-400">
            <Database className="w-5 h-5" />
          </div>
          <div>
            <div className="text-xs text-zinc-400">缓存 DB 体积</div>
            <div className="text-sm font-semibold font-mono text-zinc-200">
              {cacheSizeMb} MB
            </div>
            <div className="text-[10px] text-zinc-500">SQLite 只读加速</div>
          </div>
        </div>

        <div className="p-4 rounded-2xl bg-zinc-900/80 border border-zinc-800 flex items-center gap-3">
          <div className="w-10 h-10 rounded-xl bg-amber-950 border border-amber-500/20 flex items-center justify-center text-amber-400">
            <FolderSync className="w-5 h-5" />
          </div>
          <div>
            <div className="text-xs text-zinc-400">AList 宿主服务</div>
            <div className="text-xs font-mono text-zinc-200 truncate max-w-[130px]" title={alistHost}>
              {alistHost}
            </div>
            <div className="text-[10px] text-emerald-400">302 播放直链中转</div>
          </div>
        </div>

        <div className="p-4 rounded-2xl bg-zinc-900/80 border border-zinc-800 flex items-center justify-between">
          <div>
            <div className="text-xs text-zinc-400">全链路运行日志</div>
            <div className="text-xs text-emerald-400 font-medium mt-1">实时诊断就绪</div>
          </div>
          <button
            type="button"
            onClick={onOpenLogs}
            className="px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium transition cursor-pointer"
          >
            打开日志
          </button>
        </div>
      </div>

      {/* Monitor Roots Management */}
      <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
              <FolderSync className="w-4 h-4 text-emerald-400" />
              小雅监控目录列表 ({rootsList.length})
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              配置定时自动同步的小雅网盘目录，自动跟踪并转换为 Emby 适用的 STRM 资源
            </p>
          </div>

          <button
            type="button"
            onClick={() => setShowAddRoot(true)}
            className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md shadow-emerald-600/30 transition cursor-pointer"
          >
            <Plus className="w-4 h-4" />
            添加监控目录
          </button>
        </div>

        {rootsList.length === 0 ? (
          <div className="py-8 text-center text-zinc-500 text-xs">
            暂无监控目录。点击右上角「添加监控目录」配置定时追更路径。
          </div>
        ) : (
          <div className="divide-y divide-zinc-800/80 border border-zinc-800 rounded-xl overflow-hidden bg-zinc-950/40">
            {rootsList.map((r) => (
              <div
                key={r.id}
                className="p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3 text-xs"
              >
                <div className="flex items-center gap-3">
                  <span
                    className={`w-2.5 h-2.5 rounded-full ${
                      r.enabled ? 'bg-emerald-500 shadow-sm shadow-emerald-500' : 'bg-zinc-600'
                    }`}
                  />
                  <div>
                    <div className="font-mono text-zinc-200 font-semibold">{r.root_path}</div>
                    <div className="text-[11px] text-zinc-500 mt-0.5">
                      间隔: {r.interval_sec} 秒 ({Math.round(r.interval_sec / 60)} 分钟) · 状态:{' '}
                      <span className={r.enabled ? 'text-emerald-400' : 'text-zinc-500'}>
                        {r.enabled ? '已启用监控' : '已暂停'}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => handleToggleRoot(r.id, r.enabled, r.interval_sec)}
                    className="px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 font-medium cursor-pointer"
                  >
                    {r.enabled ? '停用' : '启用'}
                  </button>
                  <button
                    type="button"
                    onClick={() => handleTriggerSync(r.id, false)}
                    className="px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 font-medium flex items-center gap-1 cursor-pointer"
                    title="增量同步"
                  >
                    <RefreshCw className="w-3 h-3 text-emerald-400" />
                    增量
                  </button>
                  <button
                    type="button"
                    onClick={() => handleTriggerSync(r.id, true)}
                    className="px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 font-medium flex items-center gap-1 cursor-pointer"
                    title="全量同步"
                  >
                    <Play className="w-3 h-3 text-sky-400" />
                    全量
                  </button>
                  <button
                    type="button"
                    onClick={() => handleDeleteRoot(r.id, r.root_path)}
                    className="p-1 rounded-lg hover:bg-rose-950/60 text-zinc-500 hover:text-rose-400 cursor-pointer"
                    title="删除监控"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* AList File Explorer Section */}
      <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div>
            <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
              <Folder className="w-4 h-4 text-emerald-400" />
              AList 资源浏览器与按需入库
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              支持分页浏览、关键词检索、单次入库及整剧自动追更
            </p>
          </div>

          {/* Search bar */}
          <form onSubmit={handleSearchSubmit} className="flex items-center gap-2">
            <div className="relative">
              <Search className="w-3.5 h-3.5 text-zinc-500 absolute left-3 top-2.5" />
              <input
                type="text"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                placeholder="在当前路径下检索..."
                className="pl-8 pr-3 py-1.5 rounded-xl bg-zinc-950 border border-zinc-800 text-xs text-zinc-200 placeholder-zinc-500 focus:outline-none focus:border-emerald-500 w-52 sm:w-64"
              />
            </div>
            <button
              type="submit"
              className="px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium cursor-pointer"
            >
              检索
            </button>
            {(activeSearch || searchInput) && (
              <button
                type="button"
                onClick={handleClearSearch}
                className="px-2 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-400 text-xs cursor-pointer"
              >
                重置
              </button>
            )}
          </form>
        </div>

        {/* Active Search Banner */}
        {activeSearch && (
          <div className="flex items-center justify-between px-3 py-2 rounded-xl bg-emerald-950/40 border border-emerald-500/30 text-xs text-emerald-300">
            <div className="flex items-center gap-2">
              <Search className="w-3.5 h-3.5 text-emerald-400 shrink-0" />
              <span>
                正在检索关键词：<span className="font-bold text-white font-mono">{activeSearch}</span>
                {alistPath !== '/' && <span>（目录: <span className="font-mono text-zinc-300">{alistPath}</span>）</span>}
                ，共找到 <span className="font-bold text-white">{alistTotal}</span> 项结果
              </span>
            </div>
            <button
              type="button"
              onClick={handleClearSearch}
              className="text-xs text-zinc-400 hover:text-white underline cursor-pointer"
            >
              退出搜索并返回目录
            </button>
          </div>
        )}

        {/* Path Breadcrumbs */}
        <div className="flex items-center gap-1.5 p-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs overflow-x-auto">
          <button
            type="button"
            onClick={() => navigateTo('/')}
            className={`px-2 py-0.5 rounded font-mono cursor-pointer ${
              alistPath === '/' && !activeSearch ? 'bg-emerald-950 text-emerald-400 font-bold' : 'text-zinc-400 hover:text-white'
            }`}
          >
            根目录 /
          </button>
          {breadcrumbs.map((seg, idx) => {
            const currentSubPath = '/' + breadcrumbs.slice(0, idx + 1).join('/');
            const isLast = idx === breadcrumbs.length - 1;
            return (
              <React.Fragment key={seg}>
                <ChevronRight className="w-3.5 h-3.5 text-zinc-600 shrink-0" />
                <button
                  type="button"
                  onClick={() => navigateTo(currentSubPath)}
                  className={`px-2 py-0.5 rounded font-mono shrink-0 cursor-pointer ${
                    isLast && !activeSearch
                      ? 'bg-emerald-950 text-emerald-400 font-bold'
                      : 'text-zinc-400 hover:text-white'
                  }`}
                >
                  {seg}
                </button>
              </React.Fragment>
            );
          })}
        </div>

        {/* Table of items */}
        <div className="border border-zinc-800 rounded-xl overflow-hidden bg-zinc-950/40">
          <table className="w-full text-left text-xs">
            <thead className="bg-zinc-900/70 border-b border-zinc-800 text-zinc-400">
              <tr>
                <th className="px-4 py-2.5 font-medium">名称</th>
                <th className="px-4 py-2.5 font-medium w-28">大小</th>
                <th className="px-4 py-2.5 font-medium w-40">修改时间</th>
                <th className="px-4 py-2.5 font-medium text-right w-52">入库操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/60">
              {alistLoading ? (
                <tr>
                  <td colSpan={4} className="py-12 text-center text-zinc-500">
                    <RefreshCw className="w-6 h-6 animate-spin mx-auto mb-2 text-emerald-500" />
                    正在读取 AList 资源...
                  </td>
                </tr>
              ) : alistList.length === 0 ? (
                <tr>
                  <td colSpan={4} className="py-12 text-center text-zinc-500">
                    当前目录下无文件或匹配结果
                  </td>
                </tr>
              ) : (
                alistList.map((item) => (
                  <tr
                    key={item.full_path || (item.parent ? `${item.parent}/${item.name}` : item.name)}
                    className="hover:bg-zinc-900/50 transition group"
                  >
                    <td className="px-4 py-2.5 font-medium text-zinc-200">
                      <div className="flex items-center gap-2.5">
                        {item.is_dir ? (
                          <Folder className="w-4 h-4 text-amber-400 shrink-0" />
                        ) : (
                          <FileVideo className="w-4 h-4 text-sky-400 shrink-0" />
                        )}
                        {item.is_dir ? (
                          <button
                            type="button"
                            onClick={() => {
                              const target = item.full_path || (item.parent ? `${item.parent.replace(/\/$/, '')}/${item.name}` : (alistPath === '/' ? `/${item.name}` : `${alistPath}/${item.name}`));
                              navigateTo(target);
                            }}
                            className="text-left font-mono hover:text-emerald-400 transition truncate max-w-md cursor-pointer"
                          >
                            {item.name}
                          </button>
                        ) : (
                          <div className="flex flex-col">
                            <span className="font-mono text-zinc-300 truncate max-w-md">{item.name}</span>
                            {activeSearch && item.parent && (
                              <span className="text-[10px] text-zinc-500 font-mono truncate max-w-md">
                                路径: {item.parent}
                              </span>
                            )}
                          </div>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-zinc-400">
                      {item.is_dir ? '—' : formatFileSize(item.size)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-zinc-500 text-[11px]">
                      {item.modified ? item.modified.slice(0, 19).replace('T', ' ') : '—'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <div className="flex items-center justify-end gap-1.5">
                        {item.is_dir && (
                          <button
                            type="button"
                            onClick={() => handleImport(item, true)}
                            className="px-2.5 py-1 rounded-lg bg-emerald-950/80 hover:bg-emerald-900 border border-emerald-500/30 text-emerald-300 text-[11px] font-medium flex items-center gap-1 transition cursor-pointer"
                            title="自动将此目录下剧集生成 STRM 并保持追更"
                          >
                            <BookmarkPlus className="w-3 h-3" />
                            整剧追更
                          </button>
                        )}
                        <button
                          type="button"
                          onClick={() => handleImport(item, false)}
                          className="px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 text-[11px] font-medium flex items-center gap-1 transition cursor-pointer"
                          title="单次生成 STRM 入库"
                        >
                          <CheckCircle2 className="w-3 h-3 text-sky-400" />
                          单次入库
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination Bar */}
        <div className="flex items-center justify-between text-xs text-zinc-400 pt-1">
          <div>
            共 <span className="font-mono text-zinc-200">{alistTotal}</span> 项 · 当前第{' '}
            <span className="font-mono text-zinc-200">{alistPage}</span> / {totalPages} 页
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => handlePageChange(alistPage - 1)}
              disabled={alistPage <= 1 || alistLoading}
              className="flex items-center gap-1 px-3 py-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-200 disabled:opacity-40 transition cursor-pointer"
            >
              <ArrowLeft className="w-3.5 h-3.5" /> 上一页
            </button>
            <button
              type="button"
              onClick={() => handlePageChange(alistPage + 1)}
              disabled={alistPage >= totalPages || alistLoading}
              className="flex items-center gap-1 px-3 py-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-200 disabled:opacity-40 transition cursor-pointer"
            >
              下一页 <ArrowRight className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>

      {/* Add Monitor Dialog */}
      {showAddRoot && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
          <div className="w-full max-w-md bg-zinc-900 border border-zinc-800 rounded-3xl p-6 shadow-2xl space-y-4">
            <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
              <FolderSync className="w-4 h-4 text-emerald-400" />
              添加小雅监控目录
            </h3>

            <form onSubmit={handleAddRootSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
                  网盘根路径 (AList 路径)
                </label>
                <input
                  type="text"
                  required
                  value={newRootPath}
                  onChange={(e) => setNewRootPath(e.target.value)}
                  placeholder="例如: /每日更新/电视剧 或 /电影"
                  className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-100 focus:outline-none focus:border-emerald-500"
                />
              </div>

              <div>
                <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
                  同步轮询间隔 (秒)
                </label>
                <input
                  type="number"
                  required
                  min={60}
                  value={newInterval}
                  onChange={(e) => setNewInterval(Number(e.target.value))}
                  className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-sm text-zinc-100 focus:outline-none focus:border-emerald-500 font-mono"
                />
                <span className="text-[11px] text-zinc-500">建议 3600 (1小时) 或 7200 (2小时)</span>
              </div>

              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={newEnabled}
                  onChange={(e) => setNewEnabled(e.target.checked)}
                  className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-emerald-500 focus:ring-0"
                />
                <span className="text-xs text-zinc-200">立即启用监控</span>
              </label>

              <div className="flex items-center justify-end gap-3 pt-3 border-t border-zinc-800">
                <button
                  type="button"
                  onClick={() => setShowAddRoot(false)}
                  className="px-4 py-2 rounded-xl border border-zinc-800 text-zinc-400 hover:bg-zinc-800 text-xs"
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="px-5 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md shadow-emerald-600/30"
                >
                  确认添加
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};

function formatFileSize(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}
