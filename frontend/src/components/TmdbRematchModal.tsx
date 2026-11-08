import React, { useState, useEffect } from 'react';
import { Search, X, Check, Star, Calendar, RefreshCw } from 'lucide-react';
import { MediaItem, TmdbSearchResult } from '../types';
import { searchTmdb, matchTmdb, notifyToast } from '../api/client';

interface TmdbRematchModalProps {
  item: MediaItem | null;
  isOpen: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

export const TmdbRematchModal: React.FC<TmdbRematchModalProps> = ({
  item,
  isOpen,
  onClose,
  onSuccess,
}) => {
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState<'movie' | 'tv'>('movie');
  const [year, setYear] = useState('');
  const [results, setResults] = useState<TmdbSearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [matching, setMatching] = useState(false);

  useEffect(() => {
    if (item && isOpen) {
      setQuery(item.Name.replace(/\(\d{4}\)/g, '').trim());
      setKind(item.Type === 'Series' ? 'tv' : 'movie');
      setYear(item.ProductionYear ? String(item.ProductionYear) : '');
      setResults([]);
      // auto search
      handleSearch(item.Name.replace(/\(\d{4}\)/g, '').trim(), item.Type === 'Series' ? 'tv' : 'movie', item.ProductionYear ? String(item.ProductionYear) : '');
    }
  }, [item, isOpen]);

  const handleSearch = async (searchQuery: string, searchKind: 'movie' | 'tv', searchYear?: string) => {
    if (!searchQuery.trim()) return;
    setSearching(true);
    try {
      const res = await searchTmdb(searchQuery.trim(), searchKind, searchYear?.trim() || undefined);
      setResults(res || []);
    } catch (err: any) {
      notifyToast(err.message || 'TMDB 搜索失败', 'error');
    } finally {
      setSearching(false);
    }
  };

  const handleApplyMatch = async (tmdbId: number) => {
    if (!item) return;
    setMatching(true);
    try {
      await matchTmdb(item.Id, tmdbId, kind);
      notifyToast('已成功修正 TMDB 匹配并触发元数据重新抓取', 'success');
      onSuccess();
      onClose();
    } catch (err: any) {
      notifyToast(err.message || '绑定 TMDB 失败', 'error');
    } finally {
      setMatching(false);
    }
  };

  if (!isOpen || !item) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4 animate-in fade-in">
      <div className="w-full max-w-2xl bg-zinc-900 border border-zinc-800 rounded-3xl overflow-hidden shadow-2xl flex flex-col max-h-[85vh]">
        {/* Header */}
        <div className="p-5 border-b border-zinc-800 flex items-center justify-between bg-zinc-950/60">
          <div>
            <h3 className="text-base font-bold text-zinc-100 flex items-center gap-2">
              <Search className="w-4 h-4 text-emerald-400" />
              TMDB 刮削纠偏与重绑
            </h3>
            <p className="text-xs text-zinc-400 mt-0.5">
              当前条目: <span className="font-mono text-zinc-200">{item.Name}</span> ({item.Type === 'Series' ? '电视剧' : '电影'})
            </p>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-xl bg-zinc-800/80 hover:bg-zinc-700 text-zinc-400 hover:text-white transition cursor-pointer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Search Bar */}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            handleSearch(query, kind, year);
          }}
          className="p-4 border-b border-zinc-800 flex flex-wrap items-center gap-3 bg-zinc-950/30"
        >
          <div className="flex-1 min-w-[180px]">
            <input
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="输入剧名、电影名..."
              className="w-full px-3.5 py-2 rounded-xl bg-zinc-900 border border-zinc-700 text-xs text-zinc-200 focus:outline-none focus:border-emerald-500"
            />
          </div>

          <div className="flex items-center gap-2">
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value as 'movie' | 'tv')}
              className="px-3 py-2 rounded-xl bg-zinc-900 border border-zinc-700 text-xs text-zinc-300 focus:outline-none"
            >
              <option value="movie">电影 (Movie)</option>
              <option value="tv">电视剧/剧集 (TV)</option>
            </select>

            <input
              type="text"
              value={year}
              onChange={(e) => setYear(e.target.value)}
              placeholder="年份(可选)"
              className="w-20 px-2.5 py-2 rounded-xl bg-zinc-900 border border-zinc-700 text-xs text-zinc-200 focus:outline-none focus:border-emerald-500 font-mono text-center"
            />

            <button
              type="submit"
              disabled={searching}
              className="px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md shadow-emerald-600/30 transition flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
            >
              <Search className="w-3.5 h-3.5" />
              搜索
            </button>
          </div>
        </form>

        {/* Results List */}
        <div className="p-4 overflow-y-auto space-y-3 flex-1">
          {searching ? (
            <div className="py-12 text-center text-zinc-500 flex flex-col items-center gap-2">
              <RefreshCw className="w-6 h-6 animate-spin text-emerald-400" />
              <span className="text-xs">正在向 TMDB 请求匹配数据...</span>
            </div>
          ) : results.length === 0 ? (
            <div className="py-12 text-center text-zinc-500 text-xs">
              暂无匹配结果，请尝试简化搜索词或调整影视分类/年份。
            </div>
          ) : (
            results.map((res) => {
              const displayTitle = res.title || res.name || res.original_title || '未知';
              const releaseYear = (res.release_date || res.first_air_date || '').slice(0, 4) || res.year;
              const poster = res.poster_url || (res.poster_path ? `https://image.tmdb.org/t/p/w200${res.poster_path}` : null);

              return (
                <div
                  key={res.id}
                  className="p-3 rounded-2xl bg-zinc-950/70 border border-zinc-800 hover:border-emerald-500/50 transition flex items-center justify-between gap-4 group"
                >
                  <div className="flex items-center gap-3.5 min-w-0">
                    <div className="w-12 h-16 rounded-xl bg-zinc-800 overflow-hidden shrink-0 border border-zinc-700/60">
                      {poster ? (
                        <img src={poster} alt={displayTitle} className="w-full h-full object-cover" />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center text-zinc-600 text-[10px]">
                          无海报
                        </div>
                      )}
                    </div>

                    <div className="min-w-0 space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-sm text-zinc-100 truncate">{displayTitle}</span>
                        {releaseYear && (
                          <span className="text-xs text-zinc-400 font-mono">({releaseYear})</span>
                        )}
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-400 font-mono">
                          ID: {res.id}
                        </span>
                      </div>

                      <div className="flex items-center gap-3 text-xs text-zinc-400">
                        {res.rating ? (
                          <span className="flex items-center gap-1 text-amber-400 font-mono">
                            <Star className="w-3 h-3 fill-amber-400" />
                            {res.rating.toFixed(1)}
                          </span>
                        ) : null}
                        <p className="text-[11px] text-zinc-400 line-clamp-1 max-w-md">
                          {res.overview || '暂无简介'}
                        </p>
                      </div>
                    </div>
                  </div>

                  <button
                    onClick={() => handleApplyMatch(res.id)}
                    disabled={matching}
                    className="px-3.5 py-1.5 rounded-xl bg-emerald-600/90 hover:bg-emerald-500 text-white text-xs font-semibold shrink-0 flex items-center gap-1.5 transition shadow-sm cursor-pointer disabled:opacity-50"
                  >
                    <Check className="w-3.5 h-3.5" />
                    应用此匹配
                  </button>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
};
