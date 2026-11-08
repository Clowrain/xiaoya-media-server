import React, { useState, useEffect, useRef } from 'react';
import { 
  Play, Pause, Volume2, VolumeX, Maximize, Minimize, 
  RotateCcw, RotateCw, X, ArrowLeft, Star, Calendar, 
  Clock, Film, Tv, Layers, RefreshCw, Search, Edit3, Trash2, CheckCircle2,
  Copy, ExternalLink, MonitorPlay 
} from 'lucide-react';
import { User, MediaItem } from '../types';
import { 
  fetchSeasons, fetchEpisodes, fetchItemDetail, 
  refreshItemMediaInfo, renameMediaItem, deleteMediaItem, 
  getStoredToken, notifyToast 
} from '../api/client';
import { TmdbRematchModal } from './TmdbRematchModal';

interface SenPlayerModalProps {
  item: MediaItem | null;
  user: User;
  isOpen: boolean;
  onClose: () => void;
  onItemUpdated?: () => void;
}

export const SenPlayerModal: React.FC<SenPlayerModalProps> = ({
  item: initialItem,
  user,
  isOpen,
  onClose,
  onItemUpdated,
}) => {
  const [item, setItem] = useState<MediaItem | null>(initialItem);
  const [seasons, setSeasons] = useState<MediaItem[]>([]);
  const [selectedSeasonId, setSelectedSeasonId] = useState<string>('');
  const [episodes, setEpisodes] = useState<MediaItem[]>([]);
  const [loadingEpisodes, setLoadingEpisodes] = useState<boolean>(false);

  // Playback state
  const [playingItem, setPlayingItem] = useState<MediaItem | null>(null);
  const [isPlaying, setIsPlaying] = useState<boolean>(false);
  const [currentTime, setCurrentTime] = useState<number>(0);
  const [duration, setDuration] = useState<number>(0);
  const [volume, setVolume] = useState<number>(1);
  const [isMuted, setIsMuted] = useState<boolean>(false);
  const [isFullscreen, setIsFullscreen] = useState<boolean>(false);
  const [isRefreshing, setIsRefreshing] = useState<boolean>(false);
  const [showRematchModal, setShowRematchModal] = useState<boolean>(false);

  const videoRef = useRef<HTMLVideoElement>(null);
  const playerContainerRef = useRef<HTMLDivElement>(null);

  const isAdmin = user.Policy?.IsAdministrator ?? false;
  const token = getStoredToken() || '';

  // Sync item when initialItem changes
  useEffect(() => {
    setItem(initialItem);
    setPlayingItem(null);
    setSeasons([]);
    setSelectedSeasonId('');
    setEpisodes([]);
  }, [initialItem, isOpen]);

  // Load Seasons & Episodes if Series
  useEffect(() => {
    if (!item || !isOpen) return;

    if (item.Type === 'Series') {
      fetchSeasons(user.Id, item.Id)
        .then((sList) => {
          setSeasons(sList);
          if (sList.length > 0) {
            setSelectedSeasonId(sList[0].Id);
          } else {
            // fallback: fetch all episodes directly
            loadEpisodes('');
          }
        })
        .catch(() => {
          loadEpisodes('');
        });
    }
  }, [item?.Id, isOpen]);

  const loadEpisodes = async (seasonId: string) => {
    if (!item) return;
    setLoadingEpisodes(true);
    try {
      const eps = await fetchEpisodes(user.Id, item.Id, seasonId || undefined);
      setEpisodes(eps);
    } catch (err: any) {
      console.error('Failed to load episodes:', err);
    } finally {
      setLoadingEpisodes(false);
    }
  };

  useEffect(() => {
    if (item && item.Type === 'Series' && selectedSeasonId) {
      loadEpisodes(selectedSeasonId);
    }
  }, [selectedSeasonId]);

  const reloadItem = async () => {
    if (!item) return;
    try {
      const updated = await fetchItemDetail(user.Id, item.Id);
      setItem(updated);
      if (item.Type === 'Series' && selectedSeasonId) {
        loadEpisodes(selectedSeasonId);
      }
      onItemUpdated?.();
    } catch {
      // ignore
    }
  };

  // External player & stream link handlers
  const getStreamUrl = (target: MediaItem) => {
    return `${window.location.origin}/Videos/${target.Id}/stream?api_key=${token}&Static=true`;
  };

  const handleOpenExternal = (target: MediaItem, player: 'iina' | 'potplayer' | 'vlc' | 'browser') => {
    const rawUrl = getStreamUrl(target);
    if (player === 'iina') {
      window.location.href = `iina://weblink?url=${encodeURIComponent(rawUrl)}`;
      notifyToast('已唤醒 IINA 播放器', 'info');
    } else if (player === 'potplayer') {
      window.location.href = `potplayer://${encodeURIComponent(rawUrl)}`;
      notifyToast('已唤醒 PotPlayer 播放器', 'info');
    } else if (player === 'vlc') {
      window.location.href = `vlc://${encodeURIComponent(rawUrl)}`;
      notifyToast('已唤醒 VLC 播放器', 'info');
    } else {
      window.open(rawUrl, '_blank');
    }
  };

  const handleCopyStreamLink = (target: MediaItem) => {
    const rawUrl = getStreamUrl(target);
    navigator.clipboard.writeText(rawUrl);
    notifyToast('播放直链已复制到剪贴板，可粘贴至播放器', 'success');
  };

  // Video player controls
  const handleStartPlay = (target: MediaItem) => {
    handleOpenExternal(target, 'browser');
  };

  const handleStopPlay = () => {
    if (videoRef.current) {
      videoRef.current.pause();
    }
    setPlayingItem(null);
    setIsPlaying(false);
  };

  const togglePlay = () => {
    if (!videoRef.current) return;
    if (videoRef.current.paused) {
      videoRef.current.play();
      setIsPlaying(true);
    } else {
      videoRef.current.pause();
      setIsPlaying(false);
    }
  };

  const handleSeek = (seconds: number) => {
    if (!videoRef.current) return;
    videoRef.current.currentTime = Math.max(0, Math.min(videoRef.current.duration || 0, videoRef.current.currentTime + seconds));
  };

  const handleVolumeChange = (newVol: number) => {
    if (!videoRef.current) return;
    videoRef.current.volume = newVol;
    setVolume(newVol);
    setIsMuted(newVol === 0);
  };

  const toggleMute = () => {
    if (!videoRef.current) return;
    videoRef.current.muted = !isMuted;
    setIsMuted(!isMuted);
  };

  const toggleFullscreen = () => {
    if (!playerContainerRef.current) return;
    if (!document.fullscreenElement) {
      playerContainerRef.current.requestFullscreen().then(() => setIsFullscreen(true)).catch(() => {});
    } else {
      document.exitFullscreen().then(() => setIsFullscreen(false)).catch(() => {});
    }
  };

  // Admin Actions
  const handleRefreshMetadata = async () => {
    if (!item) return;
    setIsRefreshing(true);
    try {
      await refreshItemMediaInfo(item.Id);
      notifyToast('元数据刷新已触发', 'success');
      setTimeout(reloadItem, 1500);
    } catch (err: any) {
      notifyToast(err.message || '刷新元数据失败', 'error');
    } finally {
      setIsRefreshing(false);
    }
  };

  const handleRename = async () => {
    if (!item) return;
    const newName = prompt('修改影视名称:', item.Name);
    if (!newName || !newName.trim() || newName.trim() === item.Name) return;
    try {
      await renameMediaItem(item.Id, newName.trim());
      notifyToast('影视名称已更新', 'success');
      reloadItem();
    } catch (err: any) {
      notifyToast(err.message || '重命名失败', 'error');
    }
  };

  const handleDelete = async () => {
    if (!item) return;
    if (!confirm(`确定从媒体库中删除「${item.Name}」？`)) return;
    try {
      await deleteMediaItem(item.Id);
      notifyToast('已从媒体库删除', 'success');
      onItemUpdated?.();
      onClose();
    } catch (err: any) {
      notifyToast(err.message || '删除失败', 'error');
    }
  };

  if (!isOpen || !item) return null;

  // Media Specs (extract codec, resolution, container)
  const mediaSource = item.MediaSources?.[0];
  const videoStream = mediaSource?.MediaStreams?.find((s) => s.Type === 'Video');
  const audioStream = mediaSource?.MediaStreams?.find((s) => s.Type === 'Audio');
  const videoCodec = videoStream?.Codec ? videoStream.Codec.toUpperCase() : 'HEVC';
  const audioCodec = audioStream?.DisplayTitle || audioStream?.Codec?.toUpperCase() || 'AAC 5.1';
  const container = mediaSource?.Container ? mediaSource.Container.toUpperCase() : 'STRM';

  const posterUrl = item.ImageTags?.Primary
    ? `/Items/${item.Id}/Images/Primary?fillHeight=600&tag=${item.ImageTags.Primary}&quality=90`
    : null;

  const backdropUrl = item.BackdropImageTags?.[0]
    ? `/Items/${item.Id}/Images/Backdrop/0?fillWidth=1600&tag=${item.BackdropImageTags[0]}&quality=85`
    : null;

  const durationMin = item.RunTimeTicks ? Math.round(item.RunTimeTicks / 1e7 / 60) : null;

  // Video URL with authentication token for seamless in-browser playback
  const streamUrl = playingItem ? `/Videos/${playingItem.Id}/stream?api_key=${encodeURIComponent(token)}&Static=true` : '';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 backdrop-blur-xl p-2 sm:p-4 animate-in fade-in">
      <div 
        ref={playerContainerRef}
        className="w-full max-w-5xl bg-zinc-950 border border-zinc-800/80 rounded-3xl overflow-hidden shadow-2xl relative flex flex-col max-h-[92vh]"
      >
        {/* ===================== Built-in Video Player View ===================== */}
        {playingItem ? (
          <div className="relative w-full h-[65vh] sm:h-[75vh] bg-black flex flex-col justify-between">
            <video
              ref={videoRef}
              src={streamUrl}
              autoPlay
              playsInline
              className="w-full h-full object-contain"
              onTimeUpdate={() => {
                if (videoRef.current) {
                  setCurrentTime(videoRef.current.currentTime);
                  setDuration(videoRef.current.duration || 0);
                }
              }}
              onPlay={() => setIsPlaying(true)}
              onPause={() => setIsPlaying(false)}
            />

            {/* Top Bar on Player */}
            <div className="absolute top-0 inset-x-0 p-4 bg-gradient-to-b from-black/80 via-black/40 to-transparent flex items-center justify-between z-20">
              <div className="flex items-center gap-3">
                <button
                  type="button"
                  onClick={handleStopPlay}
                  className="p-2 rounded-xl bg-zinc-900/80 hover:bg-zinc-800 text-white backdrop-blur-md transition flex items-center gap-1.5 text-xs font-semibold cursor-pointer"
                >
                  <ArrowLeft className="w-4 h-4" />
                  返回详情
                </button>
                <div className="min-w-0">
                  <h3 className="text-sm font-bold text-white truncate max-w-sm sm:max-w-md">
                    {playingItem.SeriesName ? `${playingItem.SeriesName} · ` : ''}
                    {playingItem.IndexNumber !== undefined ? `E${String(playingItem.IndexNumber).padStart(2, '0')} ` : ''}
                    {playingItem.Name}
                  </h3>
                  <div className="text-[10px] text-zinc-400 font-mono flex items-center gap-2">
                    <span className="px-1.5 py-0.2 rounded bg-emerald-950 text-emerald-400 border border-emerald-500/30">
                      SenPlayer 直链播放
                    </span>
                    <span>{container}</span>
                    <span>{videoCodec}</span>
                  </div>
                </div>
              </div>

              <button
                type="button"
                onClick={handleStopPlay}
                className="p-2 rounded-xl bg-zinc-900/80 hover:bg-zinc-800 text-zinc-400 hover:text-white transition cursor-pointer"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Bottom Controls Bar */}
            <div className="absolute bottom-0 inset-x-0 p-4 bg-gradient-to-t from-black/90 via-black/60 to-transparent flex flex-col gap-2 z-20">
              {/* Progress Bar */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-mono text-zinc-300 w-12 text-right">
                  {formatSeconds(currentTime)}
                </span>
                <input
                  type="range"
                  min={0}
                  max={duration || 100}
                  value={currentTime}
                  onChange={(e) => {
                    const t = Number(e.target.value);
                    if (videoRef.current) {
                      videoRef.current.currentTime = t;
                      setCurrentTime(t);
                    }
                  }}
                  className="flex-1 h-1.5 rounded-lg bg-zinc-700 accent-emerald-500 cursor-pointer"
                />
                <span className="text-[11px] font-mono text-zinc-400 w-12">
                  {formatSeconds(duration)}
                </span>
              </div>

              {/* Action Buttons */}
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 sm:gap-3">
                  <button
                    type="button"
                    onClick={togglePlay}
                    className="p-2 rounded-xl bg-emerald-500 hover:bg-emerald-400 text-white transition cursor-pointer"
                  >
                    {isPlaying ? <Pause className="w-4 h-4 fill-white" /> : <Play className="w-4 h-4 fill-white translate-x-0.5" />}
                  </button>

                  <button
                    type="button"
                    onClick={() => handleSeek(-10)}
                    className="p-2 rounded-xl bg-zinc-800/80 hover:bg-zinc-700 text-zinc-200 transition cursor-pointer"
                    title="快退 10 秒"
                  >
                    <RotateCcw className="w-4 h-4" />
                  </button>

                  <button
                    type="button"
                    onClick={() => handleSeek(10)}
                    className="p-2 rounded-xl bg-zinc-800/80 hover:bg-zinc-700 text-zinc-200 transition cursor-pointer"
                    title="快进 10 秒"
                  >
                    <RotateCw className="w-4 h-4" />
                  </button>

                  <div className="flex items-center gap-1.5 ml-2">
                    <button
                      type="button"
                      onClick={toggleMute}
                      className="p-1.5 text-zinc-300 hover:text-white"
                    >
                      {isMuted || volume === 0 ? <VolumeX className="w-4 h-4" /> : <Volume2 className="w-4 h-4" />}
                    </button>
                    <input
                      type="range"
                      min={0}
                      max={1}
                      step={0.05}
                      value={isMuted ? 0 : volume}
                      onChange={(e) => handleVolumeChange(Number(e.target.value))}
                      className="w-16 h-1 rounded bg-zinc-700 accent-emerald-500 cursor-pointer"
                    />
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <span className="hidden sm:inline-block text-[11px] text-zinc-400 font-mono">
                    {audioCodec}
                  </span>
                  <button
                    type="button"
                    onClick={toggleFullscreen}
                    className="p-2 rounded-xl bg-zinc-800/80 hover:bg-zinc-700 text-zinc-200 transition cursor-pointer"
                  >
                    {isFullscreen ? <Minimize className="w-4 h-4" /> : <Maximize className="w-4 h-4" />}
                  </button>
                </div>
              </div>
            </div>
          </div>
        ) : (
          /* ===================== SenPlayer Detail & Showcase View ===================== */
          <>
            {/* Hero Backdrop Banner */}
            <div className="relative h-64 sm:h-80 w-full overflow-hidden bg-zinc-950 shrink-0">
              {backdropUrl ? (
                <img
                  src={backdropUrl}
                  alt={item.Name}
                  className="w-full h-full object-cover object-center filter brightness-[0.4] scale-105"
                />
              ) : (
                <div className="w-full h-full bg-gradient-to-t from-zinc-950 via-zinc-900 to-zinc-950 flex items-center justify-center">
                  <Film className="w-24 h-24 text-zinc-800" />
                </div>
              )}

              {/* Cinematic Vignette */}
              <div className="absolute inset-0 bg-gradient-to-t from-zinc-950 via-zinc-950/60 to-black/60" />

              {/* Close Button */}
              <button
                type="button"
                onClick={onClose}
                className="absolute top-4 right-4 p-2 rounded-xl bg-black/50 hover:bg-black/80 text-zinc-300 hover:text-white backdrop-blur-md border border-white/10 transition z-20 cursor-pointer"
              >
                <X className="w-5 h-5" />
              </button>

              {/* Floating Poster & Meta Header */}
              <div className="absolute bottom-5 left-5 right-5 flex items-end gap-5 z-10">
                <div className="w-28 sm:w-36 aspect-[2/3] rounded-2xl bg-zinc-900 overflow-hidden shadow-2xl border-2 border-zinc-700/60 shrink-0 relative group">
                  {posterUrl ? (
                    <img src={posterUrl} alt={item.Name} className="w-full h-full object-cover" />
                  ) : (
                    <div className="w-full h-full flex items-center justify-center text-zinc-600">
                      <Film className="w-10 h-10" />
                    </div>
                  )}
                  <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition flex items-center justify-center cursor-pointer" onClick={() => handleStartPlay(item)}>
                    <div className="w-10 h-10 rounded-full bg-emerald-500 text-white flex items-center justify-center shadow-lg">
                      <Play className="w-4 h-4 fill-white translate-x-0.5" />
                    </div>
                  </div>
                </div>

                <div className="flex-1 pb-1 space-y-2 min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="px-2 py-0.5 rounded-md bg-emerald-950/90 border border-emerald-500/40 text-[10px] font-bold text-emerald-300 font-mono">
                      SenPlayer
                    </span>
                    <span className="px-2 py-0.5 rounded-md bg-black/60 border border-white/10 text-[11px] text-zinc-300 font-medium">
                      {item.Type === 'Movie' ? '电影' : item.Type === 'Series' ? '剧集' : item.Type}
                    </span>
                    <span className="px-1.5 py-0.5 rounded-md bg-zinc-800 text-[10px] font-mono text-zinc-300 border border-zinc-700">
                      4K · {videoCodec}
                    </span>
                    <span className="px-1.5 py-0.5 rounded-md bg-zinc-800 text-[10px] font-mono text-zinc-300 border border-zinc-700">
                      {container}
                    </span>
                  </div>

                  <h2 className="text-xl sm:text-3xl font-black text-white tracking-tight drop-shadow-lg truncate">
                    {item.Name}
                  </h2>

                  <div className="flex flex-wrap items-center gap-3 text-xs text-zinc-300 font-medium">
                    {(item.ProductionYear ?? 0) > 0 && (
                      <span className="flex items-center gap-1 font-mono text-zinc-400">
                        <Calendar className="w-3.5 h-3.5" />
                        {item.ProductionYear}
                      </span>
                    )}
                    {durationMin && (
                      <span className="flex items-center gap-1 font-mono text-zinc-400">
                        <Clock className="w-3.5 h-3.5" />
                        {durationMin} 分钟
                      </span>
                    )}
                    {item.CommunityRating && (
                      <span className="flex items-center gap-1 text-amber-400 font-bold font-mono">
                        <Star className="w-3.5 h-3.5 fill-amber-400" />
                        {item.CommunityRating.toFixed(1)}
                      </span>
                    )}
                    {item.Genres && item.Genres.slice(0, 3).map((g) => (
                      <span key={g} className="text-zinc-400 text-[11px]">
                        • {g}
                      </span>
                    ))}
                  </div>

                  {/* Quick Action Button Group */}
                  <div className="flex flex-wrap items-center gap-2.5 pt-1">
                    <button
                      type="button"
                      onClick={() => {
                        const target = (item.Type === 'Series' && episodes.length > 0) ? episodes[0] : item;
                        handleOpenExternal(target, 'browser');
                      }}
                      className="px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-bold flex items-center gap-2 shadow-lg shadow-emerald-600/30 transition cursor-pointer"
                    >
                      <Play className="w-4 h-4 fill-white" />
                      {item.Type === 'Series' ? '播放第一集' : '立即播放'}
                    </button>

                    <button
                      type="button"
                      onClick={() => {
                        const target = (item.Type === 'Series' && episodes.length > 0) ? episodes[0] : item;
                        handleOpenExternal(target, 'iina');
                      }}
                      className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-semibold flex items-center gap-1.5 transition cursor-pointer border border-zinc-700"
                      title="调用 macOS 本地 IINA 播放器"
                    >
                      <MonitorPlay className="w-3.5 h-3.5 text-emerald-400" />
                      IINA
                    </button>

                    <button
                      type="button"
                      onClick={() => {
                        const target = (item.Type === 'Series' && episodes.length > 0) ? episodes[0] : item;
                        handleOpenExternal(target, 'potplayer');
                      }}
                      className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-semibold flex items-center gap-1.5 transition cursor-pointer border border-zinc-700"
                      title="调用 Windows 本地 PotPlayer 播放器"
                    >
                      <MonitorPlay className="w-3.5 h-3.5 text-amber-400" />
                      PotPlayer
                    </button>

                    <button
                      type="button"
                      onClick={() => {
                        const target = (item.Type === 'Series' && episodes.length > 0) ? episodes[0] : item;
                        handleCopyStreamLink(target);
                      }}
                      className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-semibold flex items-center gap-1.5 transition cursor-pointer border border-zinc-700"
                      title="复制小雅直链 (可粘贴到 Infuse / VLC)"
                    >
                      <Copy className="w-3.5 h-3.5 text-zinc-400" />
                      复制直链
                    </button>

                    {isAdmin && (
                      <button
                        type="button"
                        onClick={() => setShowRematchModal(true)}
                        className="px-3.5 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-semibold flex items-center gap-1.5 transition cursor-pointer border border-zinc-700"
                        title="管理员专用：纠偏 TMDB 剧名、季数与海报"
                      >
                        <Search className="w-3.5 h-3.5 text-sky-400" />
                        TMDB 刮削纠偏
                      </button>
                    )}

                    {isAdmin && (
                      <button
                        type="button"
                        onClick={handleRefreshMetadata}
                        disabled={isRefreshing}
                        className="px-3 py-2 rounded-xl bg-zinc-800/80 hover:bg-zinc-700 text-zinc-300 text-xs font-medium flex items-center gap-1.5 transition cursor-pointer border border-zinc-700/80 disabled:opacity-50"
                        title="管理员专用：重新抓取元数据"
                      >
                        <RefreshCw className={`w-3.5 h-3.5 ${isRefreshing ? 'animate-spin' : ''}`} />
                        刷新
                      </button>
                    )}
                  </div>
                </div>
              </div>
            </div>

            {/* Modal Body: Overview & Seasons/Episodes */}
            <div className="p-5 sm:p-6 overflow-y-auto space-y-6 flex-1 bg-zinc-950">
              {/* Synopsis */}
              <div>
                <h4 className="text-xs font-bold text-zinc-400 uppercase tracking-wider mb-2">
                  剧情概述
                </h4>
                <p className="text-xs sm:text-sm text-zinc-300 leading-relaxed max-w-4xl">
                  {item.Overview || '暂无剧情简介。可通过「TMDB 刮削纠偏」拉取官方中文剧集简介与演职员海报。'}
                </p>
              </div>

              {/* Series Seasons and Episodes */}
              {item.Type === 'Series' && (
                <div className="space-y-4 pt-2">
                  <div className="flex items-center justify-between">
                    <h4 className="text-xs font-bold text-zinc-300 uppercase tracking-wider flex items-center gap-1.5">
                      <Layers className="w-4 h-4 text-emerald-400" />
                      剧集列表与选集播放
                    </h4>

                    {/* Season Tabs */}
                    {seasons.length > 1 && (
                      <div className="flex items-center gap-1 p-0.5 rounded-xl bg-zinc-900 border border-zinc-800">
                        {seasons.map((s) => (
                          <button
                            key={s.Id}
                            type="button"
                            onClick={() => setSelectedSeasonId(s.Id)}
                            className={`px-3 py-1 rounded-lg text-xs font-medium transition cursor-pointer ${
                              selectedSeasonId === s.Id
                                ? 'bg-emerald-600 text-white font-semibold'
                                : 'text-zinc-400 hover:text-zinc-200'
                            }`}
                          >
                            {s.Name || `第 ${s.IndexNumber ?? 1} 季`}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* Episodes Shelf / Grid */}
                  {loadingEpisodes ? (
                    <div className="py-12 text-center text-zinc-500 flex flex-col items-center gap-2">
                      <RefreshCw className="w-6 h-6 animate-spin text-emerald-400" />
                      <span className="text-xs">正在从 Emby 提取剧集分集信息...</span>
                    </div>
                  ) : episodes.length === 0 ? (
                    <div className="py-8 text-center text-zinc-500 text-xs bg-zinc-900/40 rounded-2xl border border-zinc-800/60">
                      该季度下暂无分集数据
                    </div>
                  ) : (
                    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
                      {episodes.map((ep) => {
                        const epThumb = ep.ImageTags?.Primary
                          ? `/Items/${ep.Id}/Images/Primary?fillWidth=400&tag=${ep.ImageTags.Primary}&quality=80`
                          : (backdropUrl || posterUrl);
                        const epDurationMin = ep.RunTimeTicks ? Math.round(ep.RunTimeTicks / 1e7 / 60) : null;
                        const epNumber = ep.IndexNumber !== undefined ? `E${String(ep.IndexNumber).padStart(2, '0')}` : 'EP';

                        return (
                          <div
                            key={ep.Id}
                            onClick={() => handleStartPlay(ep)}
                            className="group p-2.5 rounded-2xl bg-zinc-900/60 border border-zinc-800/80 hover:border-emerald-500/50 hover:bg-zinc-900 transition flex flex-col justify-between gap-2.5 cursor-pointer shadow-sm hover:shadow-lg hover:shadow-black"
                          >
                            {/* Episode Thumbnail */}
                            <div className="relative aspect-video rounded-xl bg-zinc-950 overflow-hidden border border-zinc-800 group-hover:border-zinc-700 transition">
                              {epThumb ? (
                                <img
                                  src={epThumb}
                                  alt={ep.Name}
                                  loading="lazy"
                                  className="w-full h-full object-cover transition duration-300 group-hover:scale-105"
                                />
                              ) : (
                                <div className="w-full h-full flex items-center justify-center text-zinc-600">
                                  <Tv className="w-8 h-8 opacity-30" />
                                </div>
                              )}
                              <div className="absolute inset-0 bg-black/30 group-hover:bg-black/10 transition" />
                              <div className="absolute top-1.5 left-1.5 px-1.5 py-0.5 rounded bg-black/70 backdrop-blur-md text-[10px] font-mono text-zinc-200 border border-white/10 font-bold">
                                {epNumber}
                              </div>
                              <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition">
                                <div className="w-9 h-9 rounded-full bg-emerald-500 text-white flex items-center justify-center shadow-md">
                                  <Play className="w-4 h-4 fill-white translate-x-0.5" />
                                </div>
                              </div>
                              {epDurationMin && (
                                <div className="absolute bottom-1.5 right-1.5 px-1.5 py-0.5 rounded bg-black/70 backdrop-blur-md text-[9px] font-mono text-zinc-300">
                                  {epDurationMin}m
                                </div>
                              )}
                            </div>

                            {/* Episode Details */}
                            <div className="space-y-0.5">
                              <h5 className="text-xs font-bold text-zinc-200 line-clamp-1 group-hover:text-emerald-400 transition">
                                {epNumber} · {ep.Name}
                              </h5>
                              <p className="text-[10px] text-zinc-500 line-clamp-2">
                                {ep.Overview || '暂无剧情简介'}
                              </p>
                            </div>

                            {/* Quick Action Tools */}
                            <div className="flex items-center justify-between pt-1.5 border-t border-zinc-800/80" onClick={(e) => e.stopPropagation()}>
                              <button
                                type="button"
                                onClick={() => handleCopyStreamLink(ep)}
                                className="text-[10px] text-zinc-400 hover:text-white flex items-center gap-1 hover:bg-zinc-800 px-1.5 py-0.5 rounded transition cursor-pointer"
                                title="复制小雅直链"
                              >
                                <Copy className="w-3 h-3" />
                                复制直链
                              </button>
                              <div className="flex items-center gap-1">
                                <button
                                  type="button"
                                  onClick={() => handleOpenExternal(ep, 'iina')}
                                  className="text-[10px] text-emerald-400 hover:text-emerald-300 font-mono font-bold px-1.5 py-0.5 rounded hover:bg-zinc-800 transition cursor-pointer"
                                  title="唤起 macOS IINA"
                                >
                                  IINA
                                </button>
                                <button
                                  type="button"
                                  onClick={() => handleOpenExternal(ep, 'potplayer')}
                                  className="text-[10px] text-amber-400 hover:text-amber-300 font-mono font-bold px-1.5 py-0.5 rounded hover:bg-zinc-800 transition cursor-pointer"
                                  title="唤起 Windows PotPlayer"
                                >
                                  Pot
                                </button>
                              </div>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              )}

              {/* Technical Path & Admin Management Bar */}
              {isAdmin && (
                <div className="p-4 rounded-2xl bg-zinc-900/50 border border-zinc-800/80 space-y-3">
                  <div className="flex items-center justify-between text-xs text-zinc-400">
                    <span className="font-semibold text-zinc-300">管理员控制选项</span>
                    <span className="text-[10px] text-zinc-500">仅对管理员角色展示</span>
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => setShowRematchModal(true)}
                      className="px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium transition cursor-pointer flex items-center gap-1.5"
                    >
                      <Search className="w-3.5 h-3.5 text-sky-400" />
                      重新匹配 TMDB
                    </button>
                    <button
                      type="button"
                      onClick={handleRename}
                      className="px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium transition cursor-pointer flex items-center gap-1.5"
                    >
                      <Edit3 className="w-3.5 h-3.5 text-zinc-400" />
                      重命名
                    </button>
                    <button
                      type="button"
                      onClick={handleDelete}
                      className="px-3 py-1.5 rounded-xl bg-zinc-800 hover:bg-rose-950/60 text-rose-400 text-xs font-medium transition cursor-pointer flex items-center gap-1.5"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      删除
                    </button>
                  </div>

                  {item.Path && (
                    <div className="text-[11px] font-mono text-zinc-500 break-all pt-1">
                      <span className="text-zinc-400">资源文件路径:</span> {item.Path}
                    </div>
                  )}
                </div>
              )}
            </div>
          </>
        )}
      </div>

      {/* Admin TMDB Rematch Modal */}
      {isAdmin && (
        <TmdbRematchModal
          item={item}
          isOpen={showRematchModal}
          onClose={() => setShowRematchModal(false)}
          onSuccess={reloadItem}
        />
      )}
    </div>
  );
};

function formatSeconds(sec: number): string {
  if (isNaN(sec) || sec <= 0) return '00:00';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}
