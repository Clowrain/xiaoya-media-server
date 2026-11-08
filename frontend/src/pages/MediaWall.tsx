import React, { useState, useEffect } from 'react';
import { 
  Film, Search, ArrowUpDown, RefreshCw, Star, Clapperboard, 
  Play, Tv, LayoutGrid, Sparkles, ChevronRight, Info, SearchCode 
} from 'lucide-react';
import { User, LibraryView, MediaItem } from '../types';
import { 
  fetchLibraryViews, fetchMediaItems, fetchLatestItems, 
  fetchNextUpItems, notifyToast 
} from '../api/client';
import { SenPlayerModal } from '../components/SenPlayerModal';
import { TmdbRematchModal } from '../components/TmdbRematchModal';

interface MediaWallProps {
  user: User;
  onRematchTmdb?: (item: MediaItem) => void;
}

export const MediaWall: React.FC<MediaWallProps> = ({ user, onRematchTmdb }) => {
  const [views, setViews] = useState<LibraryView[]>([]);
  const [selectedViewId, setSelectedViewId] = useState<string>('');
  const [items, setItems] = useState<MediaItem[]>([]);
  const [latestItems, setLatestItems] = useState<MediaItem[]>([]);
  const [nextUpItems, setNextUpItems] = useState<MediaItem[]>([]);
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
  const [rematchItem, setRematchItem] = useState<MediaItem | null>(null);

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

  const loadShelves = async () => {
    try {
      const [latest, nextUp] = await Promise.all([
        fetchLatestItems(user.Id, selectedViewId || undefined),
        fetchNextUpItems(user.Id),
      ]);
      setLatestItems(latest);
      setNextUpItems(nextUp);
    } catch {
      // shelves fallback gracefully
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
    loadShelves();
    loadItems();
  }, [user.Id, selectedViewId, sortBy, sortOrder]);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    loadItems();
  };

  const getCardImageUrl = (item: MediaItem, preferBackdrop: boolean = false) => {
    if (preferBackdrop || cardMode === 'backdrop') {
      if (item.BackdropImageTags && item.BackdropImageTags.length > 0) {
        return `/Items/${item.Id}/Images/Backdrop?fillWidth=640&tag=${item.BackdropImageTags[0]}&quality=85`;
      }
      if (item.ImageTags?.Backdrop) {
        return `/Items/${item.Id}/Images/Backdrop?fillWidth=640&tag=${item.ImageTags.Backdrop}&quality=85`;
      }
    }
    if (item.ImageTags?.Primary) {
      return `/Items/${item.Id}/Images/Primary?fillHeight=600&tag=${item.ImageTags.Primary}&quality=85`;
    }
    return null;
  };

  // Select Hero item: first item with backdrop from latest or items
  const heroItem = (!search.trim() && (latestItems.find(i => (i.BackdropImageTags?.length ?? 0) > 0) || items.find(i => (i.BackdropImageTags?.length ?? 0) > 0))) || null;
  const heroBackdrop = heroItem?.BackdropImageTags?.[0]
    ? `/Items/${heroItem.Id}/Images/Backdrop/0?fillWidth=1920&tag=${heroItem.BackdropImageTags[0]}&quality=85`
    : null;

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 py-6 space-y-8">
      {/* ===================== SenPlayer Featured Hero Banner ===================== */}
      {heroItem && heroBackdrop && (
        <div className="relative rounded-3xl overflow-hidden bg-zinc-950 border border-zinc-800/80 shadow-2xl h-72 sm:h-96 group">
          <img
            src={heroBackdrop}
            alt={heroItem.Name}
            className="w-full h-full object-cover object-center filter brightness-[0.45] transition-transform duration-700 group-hover:scale-105"
          />

          <div className="absolute inset-0 bg-gradient-to-t from-zinc-950 via-zinc-950/40 to-transparent" />
          <div className="absolute inset-0 bg-gradient-to-r from-zinc-950 via-zinc-950/50 to-transparent" />

          {/* Featured Content */}
          <div className="absolute inset-x-6 bottom-6 sm:bottom-8 max-w-2xl space-y-3 z-10">
            <div className="flex items-center gap-2">
              <span className="px-2.5 py-0.5 rounded-lg bg-emerald-500 text-white font-bold text-[10px] uppercase tracking-wider shadow-md shadow-emerald-500/30 flex items-center gap-1">
                <Sparkles className="w-3 h-3 fill-white" />
                SenPlayer 焦点推荐
              </span>
              <span className="px-2 py-0.5 rounded-lg bg-black/60 backdrop-blur-md text-[11px] text-zinc-300 font-medium border border-white/10">
                {heroItem.Type === 'Series' ? '热门剧集' : '精选电影'}
              </span>
              {heroItem.CommunityRating && (
                <span className="flex items-center gap-1 text-amber-400 font-mono font-bold text-xs bg-black/60 backdrop-blur-md px-2 py-0.5 rounded-lg border border-amber-500/20">
                  <Star className="w-3.5 h-3.5 fill-amber-400" />
                  {heroItem.CommunityRating.toFixed(1)}
                </span>
              )}
            </div>

            <h1 className="text-2xl sm:text-4xl font-black text-white tracking-tight drop-shadow-xl line-clamp-1">
              {heroItem.Name}
            </h1>

            <p className="text-xs sm:text-sm text-zinc-300 line-clamp-2 max-w-xl leading-relaxed drop-shadow-md">
              {heroItem.Overview || '高清画质转存，Emby 协议即时直连中转，尽享沉浸式流媒体播放体验。'}
            </p>

            <div className="flex items-center gap-3 pt-1">
              <button
                type="button"
                onClick={() => setSelectedItem(heroItem)}
                className="px-5 py-2.5 rounded-2xl bg-emerald-500 hover:bg-emerald-400 text-white text-xs sm:text-sm font-bold flex items-center gap-2 shadow-xl shadow-emerald-600/30 transition transform hover:scale-105 cursor-pointer"
              >
                <Play className="w-4 h-4 fill-white" />
                立即播放
              </button>

              <button
                type="button"
                onClick={() => setSelectedItem(heroItem)}
                className="px-4 py-2.5 rounded-2xl bg-zinc-900/80 hover:bg-zinc-800 text-zinc-200 text-xs sm:text-sm font-semibold flex items-center gap-1.5 backdrop-blur-md border border-white/10 transition cursor-pointer"
              >
                <Info className="w-4 h-4" />
                选集与详情
              </button>

              {isAdmin && (
                <button
                  type="button"
                  onClick={() => setRematchItem(heroItem)}
                  className="px-3.5 py-2.5 rounded-2xl bg-zinc-900/80 hover:bg-zinc-800 text-sky-300 hover:text-white text-xs font-medium flex items-center gap-1.5 backdrop-blur-md border border-sky-500/20 transition cursor-pointer"
                  title="管理员：直接对该条目进行 TMDB 刮削纠偏"
                >
                  <SearchCode className="w-4 h-4" />
                  刮削纠偏
                </button>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ===================== SenPlayer NextUp / Continue Watching Shelf ===================== */}
      {nextUpItems.length > 0 && !search.trim() && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-bold text-zinc-200 flex items-center gap-2">
              <Tv className="w-4 h-4 text-emerald-400" />
              继续播放与追更下一集
            </h3>
            <span className="text-xs text-zinc-500 font-mono">SenPlayer NextUp</span>
          </div>

          <div className="flex items-center gap-3.5 overflow-x-auto pb-2 scrollbar-none">
            {nextUpItems.map((it) => {
              const bg = getCardImageUrl(it, true);
              const epNum = it.IndexNumber !== undefined ? `E${String(it.IndexNumber).padStart(2, '0')}` : '';

              return (
                <div
                  key={it.Id}
                  onClick={() => setSelectedItem(it)}
                  className="w-60 sm:w-72 shrink-0 aspect-video rounded-2xl overflow-hidden bg-zinc-900/80 border border-zinc-800/80 hover:border-emerald-500/60 transition group cursor-pointer relative shadow-lg"
                >
                  {bg ? (
                    <img src={bg} alt={it.Name} className="w-full h-full object-cover transition-transform duration-500 group-hover:scale-105" />
                  ) : (
                    <div className="w-full h-full flex items-center justify-center text-zinc-600 bg-zinc-900">
                      <Tv className="w-8 h-8 opacity-40" />
                    </div>
                  )}

                  <div className="absolute inset-0 bg-gradient-to-t from-black via-black/40 to-transparent" />

                  <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition">
                    <div className="w-10 h-10 rounded-full bg-emerald-500 text-white flex items-center justify-center shadow-xl">
                      <Play className="w-4 h-4 fill-white translate-x-0.5" />
                    </div>
                  </div>

                  <div className="absolute inset-x-3 bottom-2.5">
                    <div className="text-[10px] text-emerald-400 font-semibold font-mono truncate">
                      {it.SeriesName || '剧集'} {epNum}
                    </div>
                    <div className="text-xs font-bold text-white truncate">
                      {it.Name}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* ===================== SenPlayer Latest Shelf ===================== */}
      {latestItems.length > 0 && !search.trim() && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-bold text-zinc-200 flex items-center gap-2">
              <Sparkles className="w-4 h-4 text-emerald-400" />
              最新入库影视
            </h3>
            <span className="text-xs text-zinc-500 font-mono">Recently Added</span>
          </div>

          <div className="flex items-center gap-3 overflow-x-auto pb-2 scrollbar-none">
            {latestItems.slice(0, 10).map((it) => {
              const poster = it.ImageTags?.Primary
                ? `/Items/${it.Id}/Images/Primary?fillHeight=400&tag=${it.ImageTags.Primary}&quality=80`
                : null;

              return (
                <div
                  key={it.Id}
                  onClick={() => setSelectedItem(it)}
                  className="w-28 sm:w-36 shrink-0 aspect-[2/3] rounded-2xl overflow-hidden bg-zinc-900 border border-zinc-800/80 hover:border-emerald-500/60 transition group cursor-pointer relative shadow-lg"
                >
                  {poster ? (
                    <img src={poster} alt={it.Name} className="w-full h-full object-cover transition-transform duration-500 group-hover:scale-105" />
                  ) : (
                    <div className="w-full h-full flex items-center justify-center text-zinc-600 bg-zinc-900">
                      <Film className="w-8 h-8 opacity-40" />
                    </div>
                  )}

                  <div className="absolute inset-0 bg-gradient-to-t from-black via-black/40 to-transparent opacity-80 group-hover:opacity-95 transition" />

                  <div className="absolute top-2 right-2 px-1.5 py-0.5 rounded bg-black/60 backdrop-blur-md text-[9px] font-mono text-zinc-300">
                    {it.ProductionYear || '新'}
                  </div>

                  <div className="absolute inset-x-2.5 bottom-2">
                    <div className="text-[11px] font-bold text-white truncate group-hover:text-emerald-400 transition">
                      {it.Name}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* ===================== Full Media Wall & Filters ===================== */}
      <div className="space-y-4 pt-2">
        {/* Filter and Control Bar */}
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          {/* Library View Tabs */}
          <div className="flex items-center gap-2 overflow-x-auto pb-1 max-w-full scrollbar-none">
            <button
              onClick={() => setSelectedViewId('')}
              className={`px-4 py-2 rounded-xl text-xs font-semibold shrink-0 transition cursor-pointer ${
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
                className={`px-4 py-2 rounded-xl text-xs font-semibold shrink-0 transition cursor-pointer ${
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
                placeholder="搜索片名、演职员、拼音..."
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
                title="SenPlayer 经典竖版海报 (2:3)"
              >
                <LayoutGrid className="w-3.5 h-3.5" />
                <span>海报</span>
              </button>
              <button
                type="button"
                onClick={() => handleCardModeChange('backdrop')}
                className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition cursor-pointer ${
                  cardMode === 'backdrop'
                    ? 'bg-zinc-800 text-emerald-400 font-semibold shadow-sm'
                    : 'text-zinc-400 hover:text-zinc-200'
                }`}
                title="SenPlayer 宽屏剧照 (16:9)"
              >
                <Tv className="w-3.5 h-3.5" />
                <span>宽屏</span>
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
                {isAdmin 
                  ? '管理员可进入「管理控制台」配置媒体库路径，或在「小雅同步入库」中一键将 AList 资源导入生成 STRM。'
                  : '当前媒体库中暂未收录相关视频，请联系管理员添加。'}
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
                  className={`group relative rounded-2xl overflow-hidden cursor-pointer bg-zinc-950 border border-zinc-800/80 hover:border-emerald-500/60 transition-all duration-300 hover:scale-[1.03] hover:z-20 hover:shadow-2xl hover:shadow-black/90 ${
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
                    <span className="px-2 py-0.5 rounded-md bg-black/70 backdrop-blur-md text-[10px] font-semibold text-zinc-200 border border-white/10 shadow-sm">
                      {item.Type === 'Movie' ? '电影' : item.Type === 'Series' ? '剧集' : item.Type}
                    </span>

                    {item.CommunityRating && (
                      <span className="flex items-center gap-1 px-1.5 py-0.5 rounded-md bg-black/70 backdrop-blur-md text-[10px] font-mono font-bold text-amber-400 border border-amber-500/20 shadow-sm">
                        <Star className="w-3 h-3 fill-amber-400" />
                        {item.CommunityRating.toFixed(1)}
                      </span>
                    )}
                  </div>

                  {/* Center Play Button on Hover */}
                  <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity duration-300 pointer-events-none z-10">
                    <div className="w-11 h-11 rounded-full bg-emerald-500 text-white flex items-center justify-center shadow-2xl shadow-emerald-950 transform group-hover:scale-110 transition-transform duration-300">
                      <Play className="w-5 h-5 fill-white translate-x-0.5" />
                    </div>
                  </div>

                  {/* Bottom Gradient Overlay & Metadata */}
                  <div className="absolute inset-x-0 bottom-0 p-3 pt-12 bg-gradient-to-t from-black via-black/80 to-transparent flex flex-col justify-end z-10 pointer-events-none">
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
                      <span className="text-[9px] px-1.5 py-0.2 rounded bg-zinc-800 text-zinc-300 border border-zinc-700 font-mono">
                        4K
                      </span>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* ===================== SenPlayer Detail & Web Player Modal ===================== */}
      <SenPlayerModal
        item={selectedItem}
        user={user}
        isOpen={!!selectedItem}
        onClose={() => setSelectedItem(null)}
        onItemUpdated={() => {
          loadShelves();
          loadItems();
        }}
      />

      {/* ===================== Admin Direct TMDB Rematch Modal ===================== */}
      {isAdmin && (
        <TmdbRematchModal
          item={rematchItem}
          isOpen={!!rematchItem}
          onClose={() => setRematchItem(null)}
          onSuccess={() => {
            loadShelves();
            loadItems();
          }}
        />
      )}
    </div>
  );
};
