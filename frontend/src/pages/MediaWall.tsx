import React, { useState, useEffect } from 'react';
import { 
  Film, Search, ArrowUpDown, RefreshCw, Star, Clapperboard, 
  Play, Tv, LayoutGrid 
} from 'lucide-react';
import { User, LibraryView, MediaItem } from '../types';
import { fetchLibraryViews, fetchMediaItems, notifyToast } from '../api/client';
import { MediaDetailModal } from '../components/MediaDetailModal';

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
  const [cardMode, setCardMode] = useState<'poster' | 'backdrop'>(() => {
    return (localStorage.getItem('media_wall_card_mode') as 'poster' | 'backdrop') || 'poster';
  });
  const [loading, setLoading] = useState<boolean>(true);

  // Modals state
  const [selectedItem, setSelectedItem] = useState<MediaItem | null>(null);

  const isAdmin = user.Policy?.IsAdministrator ?? false;

  const handleCardModeChange = (mode: 'poster' | 'backdrop') => {
    setCardMode(mode);
    localStorage.setItem('media_wall_card_mode', mode);
  };

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
        Limit: '120',
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

  const getCardImageUrl = (item: MediaItem) => {
    if (cardMode === 'backdrop') {
      if (item.BackdropImageTags && item.BackdropImageTags.length > 0) {
        return `/Items/${item.Id}/Images/Backdrop?fillWidth=600&tag=${item.BackdropImageTags[0]}&quality=85`;
      }
      if (item.ImageTags?.Backdrop) {
        return `/Items/${item.Id}/Images/Backdrop?fillWidth=600&tag=${item.ImageTags.Backdrop}&quality=85`;
      }
    }
    if (item.ImageTags?.Primary) {
      return `/Items/${item.Id}/Images/Primary?fillHeight=600&tag=${item.ImageTags.Primary}&quality=85`;
    }
    return null;
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

        {/* Search, Sort & Aspect Ratio Controls */}
        <div className="flex items-center gap-2.5 flex-wrap">
          <form onSubmit={handleSearchSubmit} className="relative flex-1 sm:w-56">
            <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-2.5" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索影片名称、演职员..."
              className="w-full pl-9 pr-3 py-2 rounded-xl bg-zinc-900 border border-zinc-800 text-xs text-zinc-200 placeholder-zinc-500 focus:outline-none focus:border-emerald-500 transition"
            />
          </form>

          {/* Aspect Ratio Switcher (Netflix 2:3 / 16:9) */}
          <div className="flex items-center p-0.5 rounded-xl bg-zinc-900 border border-zinc-800 shrink-0">
            <button
              type="button"
              onClick={() => handleCardModeChange('poster')}
              className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition cursor-pointer ${
                cardMode === 'poster'
                  ? 'bg-zinc-800 text-emerald-400 font-semibold shadow-sm'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
              title="Netflix 经典竖版海报 (2:3)"
            >
              <LayoutGrid className="w-3.5 h-3.5" />
              <span>海报 2:3</span>
            </button>
            <button
              type="button"
              onClick={() => handleCardModeChange('backdrop')}
              className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition cursor-pointer ${
                cardMode === 'backdrop'
                  ? 'bg-zinc-800 text-emerald-400 font-semibold shadow-sm'
                  : 'text-zinc-400 hover:text-zinc-200'
              }`}
              title="Netflix 宽屏剧照 (16:9)"
            >
              <Tv className="w-3.5 h-3.5" />
              <span>宽屏 16:9</span>
            </button>
          </div>

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
              className="p-2 rounded-xl bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-300 hover:text-white transition disabled:opacity-50 cursor-pointer"
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
        <div className={`grid gap-4 ${
          cardMode === 'backdrop'
            ? 'grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5'
            : 'grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'
        }`}>
          {items.map((item) => {
            const imageUrl = getCardImageUrl(item);
            const durationMin = item.RunTimeTicks ? Math.round(item.RunTimeTicks / 1e7 / 60) : null;

            return (
              <div
                key={item.Id}
                onClick={() => setSelectedItem(item)}
                className={`group relative rounded-xl overflow-hidden cursor-pointer bg-zinc-950 border border-zinc-800/80 hover:border-emerald-500/50 transition-all duration-300 hover:scale-[1.04] hover:z-20 hover:shadow-2xl hover:shadow-black/90 ${
                  cardMode === 'backdrop' ? 'aspect-video' : 'aspect-[2/3]'
                }`}
              >
                {/* Poster / Backdrop Image */}
                {imageUrl ? (
                  <img
                    src={imageUrl}
                    alt={item.Name}
                    loading="lazy"
                    className="w-full h-full object-cover transition-transform duration-500 group-hover:scale-105"
                  />
                ) : (
                  <div className="w-full h-full flex flex-col items-center justify-center p-4 text-center text-zinc-600 bg-zinc-900/80">
                    <Film className="w-10 h-10 mb-2 opacity-30 text-zinc-400" />
                    <span className="text-xs font-semibold text-zinc-400 line-clamp-2 px-2">
                      {item.Name}
                    </span>
                  </div>
                )}

                {/* Top Floating Badges */}
                <div className="absolute top-2 inset-x-2 flex items-center justify-between pointer-events-none z-10">
                  <span className="px-2 py-0.5 rounded-md bg-black/60 backdrop-blur-md text-[10px] font-medium text-zinc-200 border border-white/10 shadow-sm">
                    {item.Type === 'Movie' ? '电影' : item.Type === 'Series' ? '剧集' : item.Type}
                  </span>

                  {item.CommunityRating && (
                    <span className="flex items-center gap-1 px-1.5 py-0.5 rounded-md bg-black/60 backdrop-blur-md text-[10px] font-mono text-amber-400 border border-amber-500/20 shadow-sm">
                      <Star className="w-3 h-3 fill-amber-400" />
                      {item.CommunityRating.toFixed(1)}
                    </span>
                  )}
                </div>

                {/* Center Play Button on Hover (Netflix Style) */}
                <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity duration-300 pointer-events-none z-10">
                  <div className="w-11 h-11 rounded-full bg-emerald-500/90 text-white flex items-center justify-center shadow-2xl shadow-emerald-950 transform group-hover:scale-105 transition-transform duration-300">
                    <Play className="w-5 h-5 fill-white translate-x-0.5" />
                  </div>
                </div>

                {/* Bottom Netflix Gradient Overlay & Metadata */}
                <div className="absolute inset-x-0 bottom-0 p-3 pt-12 bg-gradient-to-t from-black via-black/75 to-transparent flex flex-col justify-end z-10 pointer-events-none">
                  <h3 className="font-bold text-xs sm:text-sm text-white line-clamp-1 drop-shadow-md group-hover:text-emerald-400 transition">
                    {item.Name}
                  </h3>
                  <div className="flex items-center justify-between mt-1 text-[11px] text-zinc-300 font-medium">
                    <div className="flex items-center gap-2">
                      <span>{item.ProductionYear || '—'}</span>
                      {durationMin ? (
                        <span className="text-[10px] text-zinc-400 font-mono">
                          {durationMin}m
                        </span>
                      ) : null}
                    </div>
                    <span className="text-[9px] px-1 py-0.5 rounded bg-zinc-800/80 text-zinc-300 border border-zinc-700/60 font-mono">
                      HD
                    </span>
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
        onRematchTmdb={(it) => onRematchTmdb(it)}
        onItemUpdated={() => loadItems()}
      />
    </div>
  );
};
