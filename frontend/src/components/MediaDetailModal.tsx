import React, { useState } from 'react';
import { RefreshCw, Edit3, Trash2, Search, X, Star, Calendar, Clock, Film } from 'lucide-react';
import { MediaItem } from '../types';
import { notifyToast } from '../api/client';
import { refreshItemMediaInfo, renameMediaItem, deleteMediaItem, getPrimaryImageUrl, getBackdropImageUrl } from '../api/media';

interface MediaDetailModalProps {
  item: MediaItem | null;
  isOpen: boolean;
  isAdmin: boolean;
  onClose: () => void;
  onRematchTmdb: (item: MediaItem) => void;
  onItemUpdated: () => void;
}

export const MediaDetailModal: React.FC<MediaDetailModalProps> = ({
  item,
  isOpen,
  isAdmin,
  onClose,
  onRematchTmdb,
  onItemUpdated,
}) => {
  const [isRefreshing, setIsRefreshing] = useState(false);

  if (!isOpen || !item) return null;

  const handleRefresh = async () => {
    setIsRefreshing(true);
    try {
      await refreshItemMediaInfo(item.Id);
      notifyToast('元数据刷新任务已触发', 'success');
      onItemUpdated();
    } catch (err: any) {
      notifyToast(err.message || '刷新元数据失败', 'error');
    } finally {
      setIsRefreshing(false);
    }
  };

  const handleRename = async () => {
    const newName = prompt('修改影视名称:', item.Name);
    if (!newName || !newName.trim() || newName.trim() === item.Name) return;
    try {
      await renameMediaItem(item.Id, newName.trim());
      notifyToast('影视名称已更新', 'success');
      onItemUpdated();
      onClose();
    } catch (err: any) {
      notifyToast(err.message || '重命名失败', 'error');
    }
  };

  const handleDelete = async () => {
    if (!confirm(`确定从媒体库中删除「${item.Name}」？`)) return;
    try {
      await deleteMediaItem(item.Id);
      notifyToast('已从媒体库删除', 'success');
      onItemUpdated();
      onClose();
    } catch (err: any) {
      notifyToast(err.message || '删除失败', 'error');
    }
  };

  const posterUrl = item.ImageTags?.Primary
    ? getPrimaryImageUrl(item.Id, `fillHeight=600&tag=${item.ImageTags.Primary}&quality=90`)
    : null;

  const backdropUrl = item.BackdropImageTags?.[0]
    ? getBackdropImageUrl(item.Id, `fillWidth=1200&tag=${item.BackdropImageTags[0]}&quality=80`, 0)
    : null;

  const durationMin = item.RunTimeTicks ? Math.round(item.RunTimeTicks / 1e7 / 60) : null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4 animate-in fade-in">
      <div className="w-full max-w-3xl bg-zinc-900 border border-zinc-800 rounded-3xl overflow-hidden shadow-2xl relative flex flex-col max-h-[90vh]">
        {/* Backdrop Header */}
        <div className="relative h-64 sm:h-72 w-full overflow-hidden bg-zinc-950 shrink-0">
          {backdropUrl ? (
            <img
              src={backdropUrl}
              alt=""
              className="w-full h-full object-cover object-center filter brightness-[0.4]"
            />
          ) : (
            <div className="w-full h-full bg-gradient-to-t from-zinc-900 via-zinc-950 to-zinc-900 flex items-center justify-center">
              <Film className="w-20 h-20 text-zinc-800" />
            </div>
          )}

          <div className="absolute inset-0 bg-gradient-to-t from-zinc-900 via-transparent to-black/60" />

          {/* Close button */}
          <button
            onClick={onClose}
            className="absolute top-4 right-4 p-2 rounded-xl bg-black/40 hover:bg-black/60 text-zinc-300 hover:text-white backdrop-blur-md border border-white/10 transition z-10"
          >
            <X className="w-5 h-5" />
          </button>

          {/* Floating Poster & Core Meta */}
          <div className="absolute bottom-4 left-6 right-6 flex items-end gap-5">
            <div className="w-28 sm:w-36 aspect-[2/3] rounded-2xl bg-zinc-800 overflow-hidden shadow-2xl border-2 border-zinc-700/60 shrink-0">
              {posterUrl ? (
                <img src={posterUrl} alt={item.Name} className="w-full h-full object-cover" />
              ) : (
                <div className="w-full h-full flex items-center justify-center text-zinc-600">
                  <Film className="w-10 h-10" />
                </div>
              )}
            </div>

            <div className="flex-1 pb-1">
              <h2 className="text-xl sm:text-2xl font-black text-white tracking-tight drop-shadow-md">
                {item.Name}
              </h2>

              <div className="flex flex-wrap items-center gap-3 mt-2 text-xs text-zinc-300">
                {(item.ProductionYear ?? 0) > 0 && (
                  <span className="flex items-center gap-1 font-mono">
                    <Calendar className="w-3.5 h-3.5 text-zinc-400" />
                    {item.ProductionYear}
                  </span>
                )}
                {durationMin && (
                  <span className="flex items-center gap-1 font-mono">
                    <Clock className="w-3.5 h-3.5 text-zinc-400" />
                    {durationMin} 分钟
                  </span>
                )}
                {item.CommunityRating && (
                  <span className="flex items-center gap-1 text-amber-400 font-semibold font-mono">
                    <Star className="w-3.5 h-3.5 fill-amber-400" />
                    {item.CommunityRating.toFixed(1)}
                  </span>
                )}
                <span className="px-2 py-0.5 rounded bg-zinc-800/80 border border-zinc-700/60 text-[11px] text-emerald-400 font-mono">
                  {item.Type === 'Movie' ? '电影' : item.Type === 'Series' ? '电视剧' : item.Type}
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Modal Body */}
        <div className="p-6 overflow-y-auto space-y-5">
          <p className="text-sm text-zinc-400">使用 Emby 兼容客户端播放。</p>
          {/* Action Buttons */}
          <div className="flex flex-wrap items-center gap-3">
            <button
              onClick={handleRefresh}
              disabled={isRefreshing}
              className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-200 text-sm font-medium transition disabled:opacity-50"
            >
              <RefreshCw className={`w-4 h-4 ${isRefreshing ? 'animate-spin' : ''}`} />
              刷新元数据
            </button>

            {isAdmin && (
              <>
                <button
                  onClick={() => {
                    onRematchTmdb(item);
                    onClose();
                  }}
                  className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-200 text-sm font-medium transition"
                >
                  <Search className="w-4 h-4 text-sky-400" />
                  TMDB 刮削纠偏
                </button>

                <button
                  onClick={handleRename}
                  className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 text-zinc-200 text-sm font-medium transition"
                >
                  <Edit3 className="w-4 h-4 text-zinc-400" />
                  重命名
                </button>

                <button
                  onClick={handleDelete}
                  className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-zinc-800 hover:bg-rose-950/60 border border-zinc-700 hover:border-rose-800 text-rose-400 text-sm font-medium transition"
                >
                  <Trash2 className="w-4 h-4" />
                  删除
                </button>
              </>
            )}
          </div>

          {/* Genres */}
          {item.Genres && item.Genres.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {item.Genres.map((g) => (
                <span
                  key={g}
                  className="px-2.5 py-1 rounded-lg bg-zinc-800 text-zinc-300 text-xs font-medium"
                >
                  {g}
                </span>
              ))}
            </div>
          )}

          {/* Overview */}
          <div>
            <h4 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-2">
              剧情简介
            </h4>
            <p className="text-sm text-zinc-300 leading-relaxed">
              {item.Overview || '暂无剧情简介。可通过「TMDB 刮削纠偏」拉取官方中文剧情与海报。'}
            </p>
          </div>

          {/* File Path */}
          {item.Path && (
            <div className="p-3 rounded-xl bg-zinc-950 border border-zinc-800/80 text-xs font-mono text-zinc-500 break-all">
              <span className="text-zinc-400 font-semibold">存储路径:</span> {item.Path}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
