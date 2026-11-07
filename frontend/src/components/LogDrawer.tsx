import React, { useState, useEffect, useRef } from 'react';
import { Terminal, RefreshCw, Trash2, X, Download, Filter, Search, Play, Pause } from 'lucide-react';
import { fetchLogs, clearLogs, notifyToast } from '../api/client';
import { LogEntry } from '../types';

interface LogDrawerProps {
  isOpen: boolean;
  onClose: () => void;
}

export const LogDrawer: React.FC<LogDrawerProps> = ({ isOpen, onClose }) => {
  const [logs, setLogs] = useState<(string | LogEntry)[]>([]);
  const [level, setLevel] = useState<string>('');
  const [source, setSource] = useState<string>('');
  const [search, setSearch] = useState<string>('');
  const [limit, setLimit] = useState<number>(200);
  const [autoRefresh, setAutoRefresh] = useState<boolean>(true);
  const [loading, setLoading] = useState<boolean>(false);
  const [autoScroll, setAutoScroll] = useState<boolean>(true);

  const containerRef = useRef<HTMLDivElement>(null);

  const loadLogs = async () => {
    try {
      setLoading(true);
      const res = await fetchLogs(limit, level || undefined, source || undefined, search || undefined);
      setLogs(res.logs || []);
    } catch (err: any) {
      console.error('Fetch logs error:', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!isOpen) return;
    loadLogs();
  }, [isOpen, level, source, limit]);

  useEffect(() => {
    if (!isOpen || !autoRefresh) return;
    const interval = setInterval(() => {
      loadLogs();
    }, 2500);
    return () => clearInterval(interval);
  }, [isOpen, autoRefresh, level, source, limit, search]);

  useEffect(() => {
    if (autoScroll && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  const handleClear = async () => {
    if (!confirm('确定清空当前内存和磁盘中的运行日志吗？')) return;
    try {
      await clearLogs();
      setLogs([]);
      notifyToast('全链路日志已清空', 'success');
    } catch (err: any) {
      notifyToast(err.message || '清空日志失败', 'error');
    }
  };

  const handleExport = () => {
    const content = logs.map((l) => (typeof l === 'string' ? l : JSON.stringify(l))).join('\n');
    const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `xiaoya-media-server-${new Date().toISOString().slice(0, 19)}.log`;
    a.click();
    URL.revokeObjectURL(url);
    notifyToast('日志已导出', 'success');
  };

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/60 backdrop-blur-sm transition-opacity">
      <div className="w-full max-w-4xl h-full bg-zinc-950 border-l border-zinc-800 flex flex-col shadow-2xl animate-in slide-in-from-right duration-300">
        {/* Drawer Header */}
        <div className="px-6 py-4 border-b border-zinc-800/80 flex items-center justify-between bg-zinc-900/60">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-emerald-950 border border-emerald-500/30 flex items-center justify-center">
              <Terminal className="w-4 h-4 text-emerald-400" />
            </div>
            <div>
              <h2 className="text-base font-bold text-zinc-100 flex items-center gap-2">
                全链路排错日志
                <span className="text-xs px-2 py-0.5 rounded-full bg-zinc-800 text-zinc-400 font-mono">
                  {logs.length} 条记录
                </span>
              </h2>
              <p className="text-xs text-zinc-500">
                涵盖 Emby 请求、STRM 发现、小雅同步、TMDB 刮削与 AList 交互全链路
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button
              onClick={() => setAutoRefresh(!autoRefresh)}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition ${
                autoRefresh
                  ? 'bg-emerald-950/80 border-emerald-500/40 text-emerald-300'
                  : 'bg-zinc-900 border-zinc-800 text-zinc-400 hover:text-zinc-200'
              }`}
            >
              {autoRefresh ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
              {autoRefresh ? '实时更新中 (2.5s)' : '已暂停'}
            </button>

            <button
              onClick={() => loadLogs()}
              disabled={loading}
              className="p-2 rounded-lg bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-300 hover:text-white transition disabled:opacity-50"
              title="手动刷新"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            </button>

            <button
              onClick={handleExport}
              className="p-2 rounded-lg bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-300 hover:text-white transition"
              title="导出日志文件"
            >
              <Download className="w-4 h-4" />
            </button>

            <button
              onClick={handleClear}
              className="p-2 rounded-lg bg-zinc-900 border border-zinc-800 hover:bg-rose-950/50 hover:border-rose-800/60 text-rose-400 transition"
              title="清空日志"
            >
              <Trash2 className="w-4 h-4" />
            </button>

            <button
              onClick={onClose}
              className="p-2 rounded-lg bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-400 hover:text-white transition ml-2"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Filter Controls Bar */}
        <div className="px-6 py-3 border-b border-zinc-800/60 bg-zinc-900/30 flex flex-wrap items-center gap-3 text-xs">
          {/* Level Filter */}
          <div className="flex items-center gap-1.5">
            <span className="text-zinc-500">级别:</span>
            <select
              value={level}
              onChange={(e) => setLevel(e.target.value)}
              className="bg-zinc-900 border border-zinc-800 rounded-lg px-2.5 py-1 text-zinc-200 focus:outline-none focus:border-emerald-500"
            >
              <option value="">全部级别 (ALL)</option>
              <option value="INFO">INFO 正常</option>
              <option value="WARN">WARN 警告</option>
              <option value="ERROR">ERROR 错误</option>
              <option value="DEBUG">DEBUG 调试</option>
            </select>
          </div>

          {/* Source Filter */}
          <div className="flex items-center gap-1.5">
            <span className="text-zinc-500">链路源:</span>
            <select
              value={source}
              onChange={(e) => setSource(e.target.value)}
              className="bg-zinc-900 border border-zinc-800 rounded-lg px-2.5 py-1 text-zinc-200 focus:outline-none focus:border-emerald-500"
            >
              <option value="">全部模块</option>
              <option value="emby">Emby 协议服务</option>
              <option value="strm">STRM 发现/生成</option>
              <option value="sync">小雅同步引擎</option>
              <option value="tmdb">TMDB 刮削</option>
              <option value="alist">AList 客户端</option>
              <option value="scanner">媒体扫描器</option>
            </select>
          </div>

          {/* Search Input */}
          <div className="flex-1 min-w-[180px] relative">
            <Search className="w-3.5 h-3.5 text-zinc-500 absolute left-2.5 top-2" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && loadLogs()}
              placeholder="搜索关键词 (路径、错误、IP、用户等)..."
              className="w-full pl-8 pr-3 py-1 rounded-lg bg-zinc-900 border border-zinc-800 text-zinc-200 placeholder-zinc-600 focus:outline-none focus:border-emerald-500"
            />
          </div>

          {/* Limit */}
          <div className="flex items-center gap-1.5">
            <span className="text-zinc-500">条数:</span>
            <select
              value={limit}
              onChange={(e) => setLimit(Number(e.target.value))}
              className="bg-zinc-900 border border-zinc-800 rounded-lg px-2 py-1 text-zinc-200 focus:outline-none focus:border-emerald-500 font-mono"
            >
              <option value={100}>100</option>
              <option value={200}>200</option>
              <option value={500}>500</option>
              <option value={1000}>1000</option>
            </select>
          </div>

          {/* Auto Scroll Toggle */}
          <label className="flex items-center gap-1.5 text-zinc-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
              className="rounded bg-zinc-900 border-zinc-700 text-emerald-500 focus:ring-0"
            />
            滚屏锁定
          </label>
        </div>

        {/* Log Stream Content */}
        <div
          ref={containerRef}
          className="flex-1 p-4 overflow-y-auto font-mono text-xs leading-relaxed space-y-1.5 select-text"
        >
          {logs.length === 0 ? (
            <div className="h-full flex flex-col items-center justify-center text-zinc-600 gap-2">
              <Terminal className="w-10 h-10 opacity-30" />
              <span>暂无日志数据或已被清空</span>
            </div>
          ) : (
            logs.map((item, idx) => {
              if (typeof item === 'string') {
                const isErr = item.includes('ERROR') || item.includes('error') || item.includes('fail');
                const isWarn = item.includes('WARN') || item.includes('warning');
                return (
                  <div
                    key={idx}
                    className={`p-1.5 rounded hover:bg-zinc-900/60 transition break-all ${
                      isErr
                        ? 'text-rose-400 bg-rose-950/20'
                        : isWarn
                        ? 'text-amber-400 bg-amber-950/20'
                        : 'text-zinc-300'
                    }`}
                  >
                    {item}
                  </div>
                );
              }

              const lvl = (item.level || 'INFO').toUpperCase();
              const isErr = lvl === 'ERROR';
              const isWarn = lvl === 'WARN';
              const isDebug = lvl === 'DEBUG';

              return (
                <div
                  key={idx}
                  className={`p-2 rounded-lg border transition flex flex-col gap-1 ${
                    isErr
                      ? 'bg-rose-950/20 border-rose-900/40 text-rose-200'
                      : isWarn
                      ? 'bg-amber-950/20 border-amber-900/40 text-amber-200'
                      : isDebug
                      ? 'bg-zinc-900/40 border-zinc-800/40 text-zinc-400'
                      : 'bg-zinc-900/60 border-zinc-800/80 text-zinc-200'
                  }`}
                >
                  <div className="flex items-center gap-2 text-[11px] text-zinc-500">
                    <span className="text-zinc-400">{item.time || '—'}</span>
                    <span
                      className={`px-1.5 py-0.2 rounded font-bold text-[10px] ${
                        isErr
                          ? 'bg-rose-900/60 text-rose-300'
                          : isWarn
                          ? 'bg-amber-900/60 text-amber-300'
                          : isDebug
                          ? 'bg-zinc-800 text-zinc-400'
                          : 'bg-emerald-950 text-emerald-400 border border-emerald-500/20'
                      }`}
                    >
                      {lvl}
                    </span>
                    {item.source && (
                      <span className="px-1.5 py-0.2 rounded bg-zinc-800 text-sky-400 text-[10px]">
                        [{item.source}]
                      </span>
                    )}
                    {item.trace_id && (
                      <span className="text-zinc-600 text-[10px]">trace:{item.trace_id.slice(0, 8)}</span>
                    )}
                  </div>
                  <div className="text-xs break-all font-sans whitespace-pre-wrap">
                    {item.msg}
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
};
