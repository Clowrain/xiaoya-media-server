import React, { useState, useEffect } from 'react';
import { 
  Film, Search, Star, Play, Tv, ChevronRight, Layers, 
  ExternalLink, Copy, Check, Info, Sparkles, Filter 
} from 'lucide-react';
import { User, LibraryView, MediaItem } from '../types';
import { 
  fetchLibraryViews, fetchMediaItems, fetchLatestItems, 
  fetchNextUpItems, notifyToast, getStoredToken 
} from '../api/client';
import { SenPlayerModal } from '../components/SenPlayerModal';
import { TmdbRematchModal } from '../components/TmdbRematchModal';

interface MediaWallProps {
  user: User;
  activeTab?: 'home' | 'favorites';
  searchQuery?: string;
  onRematchTmdb?: (item: MediaItem) => void;
}

export const MediaWall: React.FC<MediaWallProps> = ({ 
  user, 
  activeTab = 'home',
  searchQuery = '',
  onRematchTmdb 
}) => {
  const [views, setViews] = useState<LibraryView[]>([]);
  const [selectedViewId, setSelectedViewId] = useState<string>('');
  const [nextUpItems, setNextUpItems] = useState<MediaItem[]>([]);
  const [libraryLatest, setLibraryLatest] = useState<Record<string, MediaItem[]>>({});
  const [gridItems, setGridItems] = useState<MediaItem[]>([]);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [localSearch, setLocalSearch] = useState<string>(searchQuery);
  const [loading, setLoading] = useState<boolean>(true);

  // Selected item for detail & season/episode picker
  const [selectedItem, setSelectedItem] = useState<MediaItem | null>(null);
  const [rematchItem, setRematchItem] = useState<MediaItem | null>(null);

  const isAdmin = user.Policy?.IsAdministrator ?? false;

  // 1. Load library views (My Media / 我的媒体)
  const loadViews = async () => {
    try {
      const v = await fetchLibraryViews(user.Id);
      setViews(v);
      return v;
    } catch (err: any) {
      console.error('Failed to load library views:', err);
      return [];
    }
  };

  // 2. Load Continue Watching (继续观看)
  const loadNextUp = async () => {
    try {
      const nextUp = await fetchNextUpItems(user.Id);
      setNextUpItems(nextUp);
    } catch {
      // fallback
    }
  };

  // 3. Load latest items for each library
  const loadEachLibraryLatest = async (viewList: LibraryView[]) => {
    const map: Record<string, MediaItem[]> = {};
    await Promise.all(
      viewList.map(async (v) => {
        try {
          const items = await fetchLatestItems(user.Id, v.Id);
          map[v.Id] = items;
        } catch {
          map[v.Id] = [];
        }
      })
    );
    setLibraryLatest(map);
  };

  // 4. Load filtered grid items when a view is clicked or searching
  const loadGridItems = async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams({
        UserId: user.Id,
        Recursive: 'true',
        IncludeItemTypes: 'Movie,Series',
        Fields: 'Overview,PrimaryImageAspectRatio,ProductionYear,CommunityRating,Path,MediaSources,Genres,ChildCount',
        SortBy: 'DateCreated,SortName',
        SortOrder: 'Descending',
        Limit: '120',
      });

      if (selectedViewId) {
        params.set('ParentId', selectedViewId);
      }
      if (localSearch.trim()) {
        params.set('SearchTerm', localSearch.trim());
      }
      if (activeTab === 'favorites') {
        params.set('Filters', 'IsFavorite');
      }

      const res = await fetchMediaItems(params);
      setGridItems(res.Items || []);
      setTotalCount(res.TotalRecordCount || 0);
    } catch (err: any) {
      notifyToast(err.message || '加载媒体库失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadViews().then((vList) => {
      if (vList && vList.length > 0) {
        loadEachLibraryLatest(vList);
      }
    });
    loadNextUp();
  }, [user.Id]);

  useEffect(() => {
    loadGridItems();
  }, [user.Id, selectedViewId, localSearch, activeTab]);

  useEffect(() => {
    if (searchQuery !== localSearch) {
      setLocalSearch(searchQuery);
    }
  }, [searchQuery]);

  const getPosterUrl = (item: MediaItem) => {
    if (item.ImageTags?.Primary) {
      return `/Items/${item.Id}/Images/Primary?fillHeight=450&tag=${item.ImageTags.Primary}&quality=85`;
    }
    return null;
  };

  const getBackdropUrl = (item: MediaItem) => {
    if (item.BackdropImageTags && item.BackdropImageTags.length > 0) {
      return `/Items/${item.Id}/Images/Backdrop/0?fillWidth=640&tag=${item.BackdropImageTags[0]}&quality=80`;
    }
    if (item.ImageTags?.Backdrop) {
      return `/Items/${item.Id}/Images/Backdrop?fillWidth=640&tag=${item.ImageTags.Backdrop}&quality=80`;
    }
    if (item.ImageTags?.Primary) {
      return `/Items/${item.Id}/Images/Primary?fillWidth=640&tag=${item.ImageTags.Primary}&quality=80`;
    }
    return null;
  };

  // Determine episode / 4K badge
  const renderItemBadge = (item: MediaItem) => {
    const is4K = item.Name.toLowerCase().includes('4k') || item.Name.toLowerCase().includes('2160p') || (item.Path && item.Path.toLowerCase().includes('4k'));
    
    if (item.Type === 'Series') {
      const epCount = item.RecursiveItemCount || item.EpisodeCount || item.ChildCount || 0;
      return (
        <div className="absolute top-2 right-2 flex items-center gap-1 z-10">
          {is4K && (
            <span className="px-1.5 py-0.5 rounded bg-black/80 backdrop-blur-md text-[9px] font-bold text-amber-300 border border-amber-500/40">
              4K
            </span>
          )}
          {epCount > 0 && (
            <span className="h-6 px-1.5 rounded-full bg-emerald-500 text-white font-bold text-[11px] flex items-center justify-center shadow-lg border border-black/40 min-w-6">
              {epCount}集
            </span>
          )}
        </div>
      );
    }

    if (is4K) {
      return (
        <span className="absolute top-2 right-2 px-1.5 py-0.5 rounded bg-black/80 backdrop-blur-md text-[9px] font-bold text-amber-300 border border-amber-500/40 z-10">
          4K
        </span>
      );
    }

    return null;
  };

  const isFiltering = !!selectedViewId || !!localSearch.trim() || activeTab === 'favorites';

  return (
    <div className="max-w-[1500px] mx-auto px-4 sm:px-6 lg:px-8 py-6 space-y-10">
      {/* Search Bar & Active Filter Bar if Searching */}
      {isFiltering && (
        <div className="flex flex-wrap items-center justify-between gap-4 p-4 rounded-2xl bg-zinc-900/80 border border-zinc-800 backdrop-blur-md">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-zinc-300">
              {activeTab === 'favorites' ? '我的收藏' : selectedViewId ? views.find(v => v.Id === selectedViewId)?.Name : '全部结果'}
            </span>
            <span className="text-xs px-2 py-0.5 rounded-full bg-emerald-500/20 text-emerald-400 font-mono font-bold">
              {totalCount} 部影视
            </span>
          </div>

          <div className="flex items-center gap-3">
            {selectedViewId && (
              <button
                type="button"
                onClick={() => setSelectedViewId('')}
                className="text-xs px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-300 transition cursor-pointer"
              >
                返回全部
              </button>
            )}
          </div>
        </div>
      )}

      {/* ========================================================
          1. 我的媒体 (My Media) 16:9 胶囊横版卡片
      ======================================================== */}
      {!isFiltering && views.length > 0 && (
        <section className="space-y-3">
          <h2 className="text-lg font-bold text-white tracking-tight flex items-center gap-2">
            我的媒体
          </h2>

          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-3 sm:gap-4">
            {views.map((v) => (
              <div
                key={v.Id}
                onClick={() => setSelectedViewId(selectedViewId === v.Id ? '' : v.Id)}
                className={`group relative aspect-[16/9] rounded-2xl overflow-hidden cursor-pointer transition-all duration-300 border shadow-lg ${
                  selectedViewId === v.Id
                    ? 'border-emerald-500 ring-2 ring-emerald-500/50 shadow-emerald-500/20'
                    : 'border-zinc-800/80 hover:border-zinc-700 bg-zinc-900/90 hover:scale-[1.02]'
                }`}
              >
                {/* Background Artwork or Stylized Gradient */}
                <div className="absolute inset-0 bg-gradient-to-tr from-zinc-950 via-zinc-900 to-zinc-800/80 group-hover:brightness-110 transition" />
                
                {/* Decorative Pill Badge */}
                <div className="absolute top-2.5 left-2.5">
                  <span className="px-2 py-0.5 rounded-md bg-black/60 backdrop-blur-md text-[10px] font-semibold text-zinc-400 uppercase tracking-wider">
                    {v.CollectionType === 'tvshows' ? '剧集' : v.CollectionType === 'movies' ? '电影' : '媒体库'}
                  </span>
                </div>

                {/* Library Title */}
                <div className="absolute inset-x-3 bottom-3 text-center">
                  <h3 className="text-sm font-bold text-white drop-shadow-md truncate group-hover:text-emerald-400 transition">
                    {v.Name}
                  </h3>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* ========================================================
          2. 继续观看 (Continue Watching / Next Up) 16:9 剧照
      ======================================================== */}
      {!isFiltering && nextUpItems.length > 0 && (
        <section className="space-y-3">
          <h2 className="text-lg font-bold text-white tracking-tight flex items-center gap-2">
            继续观看
          </h2>

          <div className="flex gap-4 overflow-x-auto pb-4 no-scrollbar scroll-smooth">
            {nextUpItems.map((item) => {
              const backdrop = getBackdropUrl(item);
              const progressPct = item.UserData?.PlaybackPositionTicks && item.RunTimeTicks
                ? Math.min(100, Math.round((item.UserData.PlaybackPositionTicks / item.RunTimeTicks) * 100))
                : 0;

              return (
                <div
                  key={item.Id}
                  onClick={() => setSelectedItem(item)}
                  className="w-64 sm:w-72 shrink-0 group rounded-2xl overflow-hidden bg-zinc-900/90 border border-zinc-800/80 hover:border-zinc-700 cursor-pointer shadow-lg transition-transform duration-300 hover:scale-[1.02]"
                >
                  {/* 16:9 Thumbnail Header */}
                  <div className="relative aspect-[16/9] w-full bg-zinc-950 overflow-hidden">
                    {backdrop ? (
                      <img
                        src={backdrop}
                        alt={item.Name}
                        className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-500"
                        loading="lazy"
                      />
                    ) : (
                      <div className="w-full h-full flex items-center justify-center bg-zinc-900">
                        <Film className="w-10 h-10 text-zinc-700" />
                      </div>
                    )}

                    {/* Play Hover Overlay */}
                    <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                      <div className="w-10 h-10 rounded-full bg-emerald-500 text-white flex items-center justify-center shadow-lg transform scale-90 group-hover:scale-100 transition-transform">
                        <Play className="w-5 h-5 fill-white ml-0.5" />
                      </div>
                    </div>

                    {/* Progress Bar */}
                    {progressPct > 0 && (
                      <div className="absolute inset-x-0 bottom-0 h-1 bg-black/60">
                        <div 
                          className="h-full bg-emerald-500 transition-all"
                          style={{ width: `${progressPct}%` }}
                        />
                      </div>
                    )}
                  </div>

                  {/* Title & Episode Meta */}
                  <div className="p-3 space-y-1">
                    <h3 className="text-sm font-bold text-white truncate group-hover:text-emerald-400 transition">
                      {item.SeriesName || item.Name}
                    </h3>
                    <p className="text-xs text-zinc-400 truncate">
                      {item.ParentIndexNumber != null && item.IndexNumber != null
                        ? `S${item.ParentIndexNumber}:E${item.IndexNumber} - ${item.Name}`
                        : item.Name}
                    </p>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      {/* ========================================================
          3. 最新[各媒体库] (Latest per Library) 2:3 海报墙滑轨
      ======================================================== */}
      {!isFiltering && views.map((v) => {
        const latest = libraryLatest[v.Id] || [];
        if (latest.length === 0) return null;

        return (
          <section key={v.Id} className="space-y-3">
            <div 
              className="flex items-center gap-1.5 cursor-pointer group w-fit"
              onClick={() => setSelectedViewId(v.Id)}
            >
              <h2 className="text-lg font-bold text-white tracking-tight group-hover:text-emerald-400 transition">
                {v.Name}
              </h2>
              <ChevronRight className="w-5 h-5 text-zinc-500 group-hover:text-emerald-400 transition transform group-hover:translate-x-0.5" />
            </div>

            <div className="flex gap-3.5 sm:gap-4 overflow-x-auto pb-4 no-scrollbar scroll-smooth">
              {latest.map((item) => {
                const poster = getPosterUrl(item);

                return (
                  <div
                    key={item.Id}
                    onClick={() => setSelectedItem(item)}
                    className="w-36 sm:w-44 shrink-0 group cursor-pointer space-y-2"
                  >
                    {/* 2:3 Poster Card */}
                    <div className="relative aspect-[2/3] w-full rounded-2xl overflow-hidden bg-zinc-900 border border-zinc-800/80 shadow-md transition-transform duration-300 group-hover:scale-105 group-hover:border-zinc-700">
                      {poster ? (
                        <img
                          src={poster}
                          alt={item.Name}
                          className="w-full h-full object-cover transition filter brightness-[0.95] group-hover:brightness-105"
                          loading="lazy"
                        />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center bg-zinc-900 text-zinc-700">
                          <Film className="w-12 h-12" />
                        </div>
                      )}

                      {/* Episode Circle Badge or 4K Label */}
                      {renderItemBadge(item)}

                      {/* Hover Overlay */}
                      <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-transparent to-transparent opacity-0 group-hover:opacity-100 transition-opacity flex items-end p-2.5">
                        <span className="text-[11px] font-medium text-zinc-300 line-clamp-2">
                          {item.Overview || item.Name}
                        </span>
                      </div>
                    </div>

                    {/* Movie/Series Title */}
                    <div className="px-1 text-center sm:text-left">
                      <h4 className="text-xs sm:text-sm font-semibold text-zinc-200 truncate group-hover:text-emerald-400 transition">
                        {item.Name}
                      </h4>
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        );
      })}

      {/* ========================================================
          4. 筛选/全部浏览网格 (Grid Mode when filtering or in specific view)
      ======================================================== */}
      {isFiltering && (
        <section className="space-y-4">
          {loading ? (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4">
              {Array.from({ length: 12 }).map((_, idx) => (
                <div key={idx} className="aspect-[2/3] rounded-2xl bg-zinc-900 animate-pulse border border-zinc-800" />
              ))}
            </div>
          ) : gridItems.length === 0 ? (
            <div className="py-20 text-center text-zinc-500">
              <Film className="w-12 h-12 mx-auto mb-3 text-zinc-700" />
              <p>没有找到相关影视内容</p>
            </div>
          ) : (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4">
              {gridItems.map((item) => {
                const poster = getPosterUrl(item);

                return (
                  <div
                    key={item.Id}
                    onClick={() => setSelectedItem(item)}
                    className="group cursor-pointer space-y-2"
                  >
                    <div className="relative aspect-[2/3] w-full rounded-2xl overflow-hidden bg-zinc-900 border border-zinc-800/80 shadow-md transition-transform duration-300 group-hover:scale-105 group-hover:border-zinc-700">
                      {poster ? (
                        <img
                          src={poster}
                          alt={item.Name}
                          className="w-full h-full object-cover"
                          loading="lazy"
                        />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center bg-zinc-900 text-zinc-700">
                          <Film className="w-12 h-12" />
                        </div>
                      )}

                      {renderItemBadge(item)}
                    </div>

                    <div className="px-1">
                      <h4 className="text-xs sm:text-sm font-semibold text-zinc-200 truncate group-hover:text-emerald-400 transition">
                        {item.Name}
                      </h4>
                      {item.ProductionYear && (
                        <span className="text-[11px] text-zinc-500 font-mono">
                          {item.ProductionYear}
                        </span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      )}

      {/* Detail / Episode Selection Modal */}
      {selectedItem && (
        <SenPlayerModal
          item={selectedItem}
          user={user}
          isOpen={!!selectedItem}
          onClose={() => setSelectedItem(null)}
          onItemUpdated={() => {
            loadViews();
            loadNextUp();
            loadGridItems();
          }}
        />
      )}

      {/* Admin TMDB Rematch Modal */}
      {isAdmin && rematchItem && (
        <TmdbRematchModal
          item={rematchItem}
          isOpen={!!rematchItem}
          onClose={() => setRematchItem(null)}
          onSuccess={() => {
            notifyToast('元数据纠偏已成功生效', 'success');
            loadViews();
            loadNextUp();
            loadGridItems();
          }}
        />
      )}
    </div>
  );
};
