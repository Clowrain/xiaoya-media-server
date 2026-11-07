import React, { useState, useEffect } from 'react';
import { 
  Sparkles, CheckCircle2, AlertCircle, RefreshCw, 
  Search, Link2, ExternalLink, ShieldCheck, Film 
} from 'lucide-react';
import { TmdbConfig, TmdbSearchResult, MediaItem } from '../../types';
import { 
  fetchTmdbConfig, updateTmdbConfig, testTmdb, 
  searchTmdb, matchTmdb, notifyToast 
} from '../../api/client';

interface AdminTmdbProps {
  initialMatchItem?: MediaItem | null;
  onClearInitialMatchItem?: () => void;
}

export const AdminTmdb: React.FC<AdminTmdbProps> = ({ 
  initialMatchItem, 
  onClearInitialMatchItem 
}) => {
  const [config, setConfig] = useState<TmdbConfig>({
    Enabled: true,
    ApiKey: '',
    BaseURL: 'https://api.themoviedb.org/3',
    ProxyURL: '',
    RateLimitMs: 250,
    AutoScrape: true,
  });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  // Test connection state
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; msg: string; latency?: number } | null>(null);

  // Manual Rematch Search state
  const [searchTargetItemId, setSearchTargetItemId] = useState<string>('');
  const [searchTargetItemName, setSearchTargetItemName] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [searchKind, setSearchKind] = useState<string>('movie');
  const [searchYear, setSearchYear] = useState<string>('');
  const [searchResults, setSearchResults] = useState<TmdbSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [matchingId, setMatchingId] = useState<number | null>(null);

  useEffect(() => {
    loadConfig();
  }, []);

  useEffect(() => {
    if (initialMatchItem) {
      setSearchTargetItemId(initialMatchItem.Id);
      setSearchTargetItemName(initialMatchItem.Name);
      setSearchQuery(initialMatchItem.Name.replace(/[\[\(].*?[\]\)]/g, '').trim());
      setSearchKind(initialMatchItem.Type === 'Series' ? 'tv' : 'movie');
      if (initialMatchItem.ProductionYear) {
        setSearchYear(String(initialMatchItem.ProductionYear));
      }
      // Trigger search automatically
      doSearch(
        initialMatchItem.Name.replace(/[\[\(].*?[\]\)]/g, '').trim(),
        initialMatchItem.Type === 'Series' ? 'tv' : 'movie',
        initialMatchItem.ProductionYear ? String(initialMatchItem.ProductionYear) : undefined
      );
      if (onClearInitialMatchItem) onClearInitialMatchItem();
    }
  }, [initialMatchItem]);

  const loadConfig = async () => {
    setLoading(true);
    try {
      const data = await fetchTmdbConfig();
      setConfig(data);
    } catch (err: any) {
      notifyToast(err.message || '加载 TMDB 配置失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await updateTmdbConfig(config);
      notifyToast('TMDB 配置已保存', 'success');
      loadConfig();
    } catch (err: any) {
      notifyToast(err.message || '保存配置失败', 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    const start = Date.now();
    try {
      await testTmdb();
      const elapsed = Date.now() - start;
      setTestResult({
        success: true,
        msg: 'TMDB API 连通性测试成功！元数据接口响应正常',
        latency: elapsed,
      });
      notifyToast('TMDB 连通性测试通过', 'success');
    } catch (err: any) {
      setTestResult({
        success: false,
        msg: err.message || '网络连接超时 (请检查 NAS 局域网代理 ProxyURL 或 API Key 是否有效)',
      });
      notifyToast('TMDB 连通测试失败', 'error');
    } finally {
      setTesting(false);
    }
  };

  const doSearch = async (q: string = searchQuery, kind: string = searchKind, year?: string) => {
    if (!q.trim()) return;
    setSearching(true);
    try {
      const res = await searchTmdb(q.trim(), kind, year || searchYear || undefined);
      setSearchResults(res);
    } catch (err: any) {
      notifyToast(err.message || 'TMDB 搜索失败', 'error');
    } finally {
      setSearching(false);
    }
  };

  const handleMatch = async (tmdbId: number) => {
    if (!searchTargetItemId) {
      notifyToast('请先填写需纠偏的媒体库条目 ID', 'error');
      return;
    }
    setMatchingId(tmdbId);
    try {
      await matchTmdb(searchTargetItemId, tmdbId, searchKind);
      notifyToast(`已成功关联 TMDB ID ${tmdbId}，刮削元数据与海报刷新中`, 'success');
    } catch (err: any) {
      notifyToast(err.message || '关联失败', 'error');
    } finally {
      setMatchingId(null);
    }
  };

  return (
    <div className="space-y-6">
      {/* TMDB Configuration Card */}
      <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-5">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-zinc-800/80 pb-4">
          <div>
            <h2 className="text-base font-bold text-zinc-100 flex items-center gap-2">
              <Sparkles className="w-5 h-5 text-amber-400" />
              TMDB 刮削与识别服务
            </h2>
            <p className="text-xs text-zinc-400 mt-0.5">
              为入库影片自动抓取官方中文标题、剧情简介、演职员列表、海报与剧照
            </p>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleTest}
              disabled={testing}
              className="flex items-center gap-1.5 px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium transition disabled:opacity-50"
            >
              <RefreshCw className={`w-3.5 h-3.5 text-amber-400 ${testing ? 'animate-spin' : ''}`} />
              {testing ? '正在测试...' : '测试连通性'}
            </button>
          </div>
        </div>

        {/* Connectivity Test Alert */}
        {testResult && (
          <div
            className={`p-4 rounded-xl border text-xs flex items-start gap-3 transition-all ${
              testResult.success
                ? 'bg-emerald-950/60 border-emerald-500/30 text-emerald-300'
                : 'bg-rose-950/60 border-rose-500/30 text-rose-300'
            }`}
          >
            {testResult.success ? (
              <CheckCircle2 className="w-5 h-5 text-emerald-400 shrink-0 mt-0.5" />
            ) : (
              <AlertCircle className="w-5 h-5 text-rose-400 shrink-0 mt-0.5" />
            )}
            <div className="space-y-1">
              <div className="font-semibold flex items-center gap-2">
                {testResult.success ? '连通性正常' : '连通失败'}
                {testResult.latency && (
                  <span className="font-mono text-[11px] px-1.5 py-0.2 rounded bg-black/40">
                    {testResult.latency}ms
                  </span>
                )}
              </div>
              <div>{testResult.msg}</div>
            </div>
          </div>
        )}

        {/* Settings Form */}
        <form onSubmit={handleSave} className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="col-span-full">
            <label className="flex items-center gap-2.5 cursor-pointer">
              <input
                type="checkbox"
                checked={config.Enabled}
                onChange={(e) => setConfig({ ...config, Enabled: e.target.checked })}
                className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-emerald-500 focus:ring-0"
              />
              <span className="text-sm font-semibold text-zinc-200">启用 TMDB 刮削引擎</span>
            </label>
          </div>

          <div>
            <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
              TheMovieDb API Key (v3 auth)
            </label>
            <input
              type="password"
              value={config.ApiKey}
              onChange={(e) => setConfig({ ...config, ApiKey: e.target.value })}
              placeholder="请输入 TMDB API Key"
              className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-100 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
              网络代理 ProxyURL (解决国内连通超时)
            </label>
            <input
              type="text"
              value={config.ProxyURL}
              onChange={(e) => setConfig({ ...config, ProxyURL: e.target.value })}
              placeholder="例如: http://192.168.1.10:7890 或 socks5://..."
              className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-100 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
              TMDB API Base URL
            </label>
            <input
              type="text"
              value={config.BaseURL}
              onChange={(e) => setConfig({ ...config, BaseURL: e.target.value })}
              placeholder="https://api.themoviedb.org/3"
              className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-100 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-zinc-400 mb-1.5">
              请求速率限制 (毫秒/次)
            </label>
            <input
              type="number"
              min={50}
              value={config.RateLimitMs}
              onChange={(e) => setConfig({ ...config, RateLimitMs: Number(e.target.value) })}
              className="w-full px-3.5 py-2 rounded-xl bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-100 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div className="col-span-full pt-2 flex items-center justify-end">
            <button
              type="submit"
              disabled={saving}
              className="px-5 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-lg shadow-emerald-600/30 transition disabled:opacity-50"
            >
              {saving ? '正在保存...' : '保存 TMDB 配置'}
            </button>
          </div>
        </form>
      </div>

      {/* Manual Rematch & Search Section */}
      <div className="bg-zinc-900/60 border border-zinc-800 rounded-2xl p-5 space-y-4">
        <div>
          <h3 className="text-sm font-bold text-zinc-100 flex items-center gap-2">
            <Search className="w-4 h-4 text-sky-400" />
            手动 TMDB 检索与识别纠偏
          </h3>
          <p className="text-xs text-zinc-400 mt-0.5">
            针对识别错误、同名电影或未能自动匹配的影片，手动搜索官方 TMDB 条目并一键修正
          </p>
        </div>

        {/* Search Bar */}
        <div className="grid grid-cols-1 sm:grid-cols-12 gap-3 bg-zinc-950/60 p-3.5 rounded-xl border border-zinc-800">
          <div className="sm:col-span-4">
            <label className="block text-[11px] text-zinc-500 mb-1">媒体库 Item ID (被纠偏项)</label>
            <input
              type="text"
              value={searchTargetItemId}
              onChange={(e) => setSearchTargetItemId(e.target.value)}
              placeholder="可从影视墙点击进入直接带入"
              className="w-full px-3 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-xs font-mono text-emerald-400 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div className="sm:col-span-4">
            <label className="block text-[11px] text-zinc-500 mb-1">搜索片名 / 剧名</label>
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && doSearch()}
              placeholder="例如: 流浪地球、繁花"
              className="w-full px-3 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-xs text-zinc-100 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div className="sm:col-span-2">
            <label className="block text-[11px] text-zinc-500 mb-1">类型</label>
            <select
              value={searchKind}
              onChange={(e) => setSearchKind(e.target.value)}
              className="w-full px-3 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-xs text-zinc-200 focus:outline-none focus:border-emerald-500"
            >
              <option value="movie">电影 (Movie)</option>
              <option value="tv">电视剧 (TV)</option>
            </select>
          </div>

          <div className="sm:col-span-2 flex items-end">
            <button
              type="button"
              onClick={() => doSearch()}
              disabled={searching}
              className="w-full flex items-center justify-center gap-1.5 py-1.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-medium transition disabled:opacity-50"
            >
              <Search className={`w-3.5 h-3.5 ${searching ? 'animate-spin' : ''}`} />
              {searching ? '检索中…' : '检索 TMDB'}
            </button>
          </div>
        </div>

        {/* Results List */}
        {searchResults.length > 0 && (
          <div className="space-y-3 pt-2">
            <div className="text-xs text-zinc-400 font-semibold">
              搜索结果 (找到 {searchResults.length} 个候选匹配):
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3 max-h-96 overflow-y-auto p-1">
              {searchResults.map((r) => {
                const title = r.title || r.name || '未知片名';
                const date = r.year ? String(r.year) : (r.release_date || r.first_air_date || '');
                const posterUrl = r.poster_url || (r.poster_path
                  ? `https://image.tmdb.org/t/p/w185${r.poster_path}`
                  : null);

                return (
                  <div
                    key={r.id}
                    className="p-3 rounded-xl bg-zinc-950 border border-zinc-800/80 hover:border-zinc-700 flex gap-3 group"
                  >
                    <div className="w-16 h-24 rounded-lg bg-zinc-900 overflow-hidden shrink-0">
                      {posterUrl ? (
                        <img src={posterUrl} alt={title} className="w-full h-full object-cover" />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center text-zinc-700">
                          <Film className="w-6 h-6" />
                        </div>
                      )}
                    </div>

                    <div className="flex-1 flex flex-col justify-between min-w-0">
                      <div>
                        <div className="flex items-center justify-between gap-2">
                          <h4 className="text-xs font-bold text-zinc-200 truncate group-hover:text-emerald-400">
                            {title}
                          </h4>
                          <span className="text-[10px] font-mono text-zinc-500 shrink-0">
                            ID: {r.id}
                          </span>
                        </div>
                        <div className="text-[11px] text-zinc-500 mt-0.5">{date.slice(0, 4)}</div>
                        <p className="text-[11px] text-zinc-400 line-clamp-2 mt-1">
                          {r.overview || '暂无简介'}
                        </p>
                      </div>

                      <div className="pt-2 flex items-center justify-end">
                        <button
                          type="button"
                          onClick={() => handleMatch(r.id)}
                          disabled={matchingId === r.id || !searchTargetItemId}
                          className="flex items-center gap-1 px-3 py-1 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-[11px] font-medium shadow-sm transition disabled:opacity-40"
                        >
                          <Link2 className="w-3 h-3" />
                          {matchingId === r.id ? '关联中…' : '关联此条目'}
                        </button>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
};
