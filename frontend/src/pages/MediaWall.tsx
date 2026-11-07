import React, { useState, useEffect } from 'react';
import { Film, Search, ArrowUpDown, Play, RefreshCw, Star, Clapperboard } from 'lucide-react';
import { User, LibraryView, MediaItem } from '../types';
import { fetchLibraryViews, fetchMediaItems, notifyToast } from '../api/client';
import { MediaDetailModal } from '../components/MediaDetailModal';
import { PlayerModal } from '../components/PlayerModal';

interface MediaWallProps {
  user: User;
  onRematchTmdb: (item: MediaItem) => void;
}

export const MediaWall: React.FC<MediaWallProps> = ({ user, onRematchTmdb }) => {
  const [views, setViews] = useState<LibraryView[]>([]);
  const [selectedViewId, setSelectedViewId] = useState<string>('');
  const [items, setItems] = useState<MediaItem[]>([]);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [search, setSearch] = useState<string>('');
  const [sortBy, setSortBy] = useState<string>('DateCreated,SortName');
  const [sortOrder, setSortOrder] = useState<string>('Descending');
  const [loading, setLoading] = useState<boolean>(true);

  // Modals state
  const [selectedItem, setSelectedItem] = useState<MediaItem | null>(null);
  const [playingItem, setPlayingItem] = useState<MediaItem | null>(null);

  const isAdmin = user.Policy?.IsAdministrator ?? false;

  const loadViews = async () => {
    try {
      const v = await fetchLibraryViews(user.Id);
      setViews(v);
    } catch (err: any) {
      console.error('Failed to load library views:', err);
    }
  };

  const loadItems = async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams({
        UserId: user.Id,
        Recursive: 'true',
        IncludeItemTypes: 'Movie,Series',
        Fields: 'Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,Path,MediaSources,Genres',
        SortBy: sortBy,
        SortOrder: sortOrder,
        Limit: '100',
      });

      if (selectedViewId) {
        params.set('ParentId', selectedViewId);
      }
      if (search.trim()) {
        params.set('SearchTerm', search.trim());
      }

      const res = await fetchMediaItems(params);
      setItems(res.Items || []);
      setTotalCount(res.TotalRecordCount || 0);
    } catch (err: any) {
      notifyToast(err.message || '加载媒体库失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadViews();
  }, [user.Id]);

  useEffect(() => {
    loadItems();
  }, [user.Id, selectedViewId, sortBy, sortOrder]);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    loadItems();
  };

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 py-6 space-y-6">
      {/* Filter and Control Bar */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        {/* Library View Tabs */}
        <div className="flex items-center gap-2 overflow-x-auto pb-1 max-w-full">
          <button
            onClick={() => setSelectedViewId('')}
            className={`px-4 py-2 rounded-xl text-xs font-semibold shrink-0 transition ${
              selectedViewId === ''
                ? 'bg-emerald-600 text-white shadow-lg shadow-emerald-600/30'
                : 'bg-zinc-900 hover:bg-zinc-800 text-zinc-300 border border-zinc-800'
            }`}
          >
            全部媒体 ({totalCount})
          </button>
          {views.map((v) => (
            <button
              key={v.Id}
              onClick={() => setSelectedViewId(v.Id)}
              className={`px-4 py-2 rounded-xl text-xs font-semibold shrink-0 transition ${
                selectedViewId === v.Id
                  ? 'bg-emerald-600 text-white shadow-lg shadow-emerald-600/30'
                  : 'bg-zinc-900 hover:bg-zinc-800 text-zinc-300 border border-zinc-800'
              }`}
            >
              {v.Name}
            </button>
          ))}
        </div>

        {/* Search & Sort Controls */}
        <div className="flex items-center gap-3">
          <form onSubmit={handleSearchSubmit} className="relative flex-1 sm:w-64">
            <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索影片名称、演职员..."
              className="w-full pl-9 pr-3 py-2 rounded-xl bg-zinc-900 border border-zinc-800 text-xs text-zinc-200 placeholder-zinc-500 focus:outline-none focus:border-emerald-500 transition"
            />
          </form>

          {/* Sort Selector */}
          <div className="flex items-center gap-1.5 shrink-0">
            <select
              value={`${sortBy}:${sortOrder}`}
              onChange={(e) => {
                const [by, order] = e.target.value.split(':');
                setSortBy(by);
                setSortOrder(order);
              }}
              className="bg-zinc-900 border border-zinc-800 rounded-xl px-3 py-2 text-xs text-zinc-300 focus:outline-none focus:border-emerald-500"
            >
              <option value="DateCreated,SortName:Descending">最新入库</option>
              <option value="SortName:Ascending">名称 (A-Z)</option>
              <option value="ProductionYear,SortName:Descending">发行年份 (从新到旧)</option>
              <option value="CommunityRating,SortName:Descending">评分最高</option>
            </select>

            <button
              onClick={() => loadItems()}
              disabled={loading}
              className="p-2 rounded-xl bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-300 hover:text-white transition disabled:opacity-50"
              title="刷新列表"
            >
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
            </button>
          </div>
        </div>
      </div>

      {/* Media Cards Grid */}
      {loading ? (
        <div className="py-24 text-center text-zinc-500 flex flex-col items-center gap-3">
          <RefreshCw className="w-8 h-8 animate-spin text-emerald-500" />
          <span className="text-sm">正在检索媒体库影视资产...</span>
        </div>
      ) : items.length === 0 ? (
        <div className="py-24 text-center text-zinc-500 flex flex-col items-center gap-4">
          <Clapperboard className="w-16 h-16 opacity-30 text-zinc-400" />
          <div className="space-y-1">
            <p className="text-base font-semibold text-zinc-300">媒体库暂无匹配影视</p>
            <p className="text-xs text-zinc-500 max-w-sm">
              可进入「管理控制台」配置媒体库路径，或在「小雅同步入库」中一键将 AList 资源导入生成 STRM。
            </p>
          </div>
        </div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
          {items.map((item) => {
            const posterUrl = item.ImageTags?.Primary
              ? `/Items/${item.Id}/Images/Primary?fillHeight=450&tag=${item.ImageTags.Primary}&quality=80`
              : null;

            return (
              <div
                key={item.Id}
                onClick={() => setSelectedItem(item)}
                className="group relative rounded-2xl bg-zinc-900/80 border border-zinc-800/80 overflow-hidden cursor-pointer hover:border-zinc-700 hover:shadow-2xl hover:shadow-emerald-950/30 transition-all duration-300 flex flex-col"
              >
                {/* Poster Container */}
                <div className="relative aspect-[2/3] bg-zinc-950 overflow-hidden">
                  {posterUrl ? (
                    <img
                      src={posterUrl}
                      alt={item.Name}
                      loading="lazy"
                      className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-500"
                    />
                  ) : (
                    <div className="w-full h-full flex flex-col items-center justify-center p-4 text-center text-zinc-600 bg-zinc-900/50">
                      <Film className="w-10 h-10 mb-2 opacity-50" />
                      <span className="text-[11px] font-medium text-zinc-400 line-clamp-2">
                        {item.Name}
                      </span>
                    </div>
                  )}

                  {/* Gradient Overlay */}
                  <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-transparent to-black/20 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setPlayingItem(item);
                      }}
                      className="w-12 h-12 rounded-full bg-emerald-600/90 text-white flex items-center justify-center shadow-lg shadow-emerald-500/40 hover:bg-emerald-500 hover:scale-110 transition transform"
                    >
                      <Play className="w-5 h-5 fill-white ml-0.5" />
                    </button>
                  </div>

                  {/* Type Badge */}
                  <span className="absolute top-2 left-2 px-2 py-0.5 rounded-md bg-black/60 backdrop-blur-md text-[10px] font-mono text-zinc-300 border border-white/10">
                    {item.Type === 'Movie' ? '电影' : item.Type === 'Series' ? '剧集' : item.Type}
                  </span>

                  {/* Rating Badge */}
                  {item.CommunityRating && (
                    <span className="absolute top-2 right-2 flex items-center gap-1 px-1.5 py-0.5 rounded-md bg-black/60 backdrop-blur-md text-[10px] font-mono text-amber-400 border border-amber-500/20">
                      <Star className="w-3 h-3 fill-amber-400" />
                      {item.CommunityRating.toFixed(1)}
                    </span>
                  )}
                </div>

                {/* Meta details */}
                <div className="p-3 flex-1 flex flex-col justify-between">
                  <h3 className="font-semibold text-xs text-zinc-200 line-clamp-1 group-hover:text-emerald-400 transition">
                    {item.Name}
                  </h3>
                  <div className="flex items-center justify-between mt-1 text-[11px] text-zinc-500 font-mono">
                    <span>{item.ProductionYear || '—'}</span>
                    <span>{item.RunTimeTicks ? `${Math.round(item.RunTimeTicks / 1e7 / 60)}m` : ''}</span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Modals */}
      <MediaDetailModal
        item={selectedItem}
        isOpen={!!selectedItem}
        isAdmin={isAdmin}
        onClose={() => setSelectedItem(null)}
        onPlay={(it) => setPlayingItem(it)}
        onRematchTmdb={(it) => onRematchTmdb(it)}
        onItemUpdated={() => loadItems()}
      />

      {playingItem && (
        <PlayerModal
          item={playingItem}
          isOpen={!!playingItem}
          onClose={() => setPlayingItem(null)}
        />
      )}
    </div>
  );
};
