import React, { useEffect, useRef, useState } from 'react';
import { X, Play, RefreshCw, AlertCircle } from 'lucide-react';
import { MediaItem, MediaSource } from '../types';
import { fetchPlaybackInfo, reportPlaybackProgress, reportPlaybackStopped, notifyToast } from '../api/client';

interface PlayerModalProps {
  item: MediaItem;
  isOpen: boolean;
  onClose: () => void;
}

export const PlayerModal: React.FC<PlayerModalProps> = ({ item, isOpen, onClose }) => {
  const [sources, setSources] = useState<MediaSource[]>([]);
  const [selectedSourceId, setSelectedSourceId] = useState<string>('');
  const [playUrl, setPlayUrl] = useState<string>('');
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);

  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    if (!isOpen) return;

    let isMounted = true;
    const initPlayback = async () => {
      setLoading(true);
      setError(null);
      try {
        const info = await fetchPlaybackInfo(item.Id);
        if (!isMounted) return;

        const mediaSources: MediaSource[] = info.MediaSources || [];
        setSources(mediaSources);

        const currentSource = mediaSources[0];
        const sourceId = currentSource ? currentSource.Id : item.Id;
        setSelectedSourceId(sourceId);

        // Build stream URL
        const streamUrl = `/videos/${item.Id}/stream?MediaSourceId=${sourceId}&Static=true`;
        setPlayUrl(streamUrl);
      } catch (err: any) {
        if (isMounted) setError(err.message || '获取播放流失败');
      } finally {
        if (isMounted) setLoading(false);
      }
    };

    initPlayback();

    return () => {
      isMounted = false;
      reportPlaybackStopped(item.Id);
    };
  }, [isOpen, item.Id]);

  // Periodic progress reporting
  useEffect(() => {
    if (!isOpen) return;
    const interval = setInterval(() => {
      const v = videoRef.current;
      if (v && !v.paused && v.currentTime > 0) {
        reportPlaybackProgress(item.Id, v.currentTime, v.duration || 0);
      }
    }, 5000);

    return () => clearInterval(interval);
  }, [isOpen, item.Id]);

  if (!isOpen) return null;

  const handleSourceChange = (sourceId: string) => {
    setSelectedSourceId(sourceId);
    setPlayUrl(`/videos/${item.Id}/stream?MediaSourceId=${sourceId}&Static=true`);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/90 backdrop-blur-md p-4 animate-in fade-in">
      <div className="w-full max-w-5xl bg-zinc-950 border border-zinc-800 rounded-3xl overflow-hidden shadow-2xl flex flex-col">
        {/* Header */}
        <div className="px-6 py-4 flex items-center justify-between border-b border-zinc-800 bg-zinc-900/50">
          <div className="flex items-center gap-3">
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-500 animate-pulse" />
            <h3 className="text-base font-bold text-zinc-100 truncate max-w-md">
              {item.Name}
            </h3>
            {sources.length > 1 && (
              <select
                value={selectedSourceId}
                onChange={(e) => handleSourceChange(e.target.value)}
                className="bg-zinc-800 border border-zinc-700 text-xs text-zinc-200 rounded-lg px-2.5 py-1 focus:outline-none focus:border-emerald-500"
              >
                {sources.map((s, idx) => (
                  <option key={s.Id || idx} value={s.Id}>
                    {s.Name || `版本 ${idx + 1}`} ({s.Container || '自动'})
                  </option>
                ))}
              </select>
            )}
          </div>
          <button
            onClick={() => {
              reportPlaybackStopped(item.Id);
              onClose();
            }}
            className="p-1.5 rounded-xl bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-400 hover:text-white transition"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Player View */}
        <div className="relative aspect-video bg-black flex items-center justify-center">
          {loading ? (
            <div className="flex flex-col items-center gap-3 text-zinc-400">
              <RefreshCw className="w-8 h-8 animate-spin text-emerald-500" />
              <span className="text-sm">正在加载视频流...</span>
            </div>
          ) : error ? (
            <div className="flex flex-col items-center gap-3 text-rose-400 p-6 text-center">
              <AlertCircle className="w-10 h-10" />
              <span className="text-sm">{error}</span>
              <p className="text-xs text-zinc-500 max-w-md">
                提示：若为 STRM 资源，请确认 AList 302 直链可正常解析，或配置本地转码支持。
              </p>
            </div>
          ) : (
            <video
              ref={videoRef}
              src={playUrl}
              controls
              autoPlay
              className="w-full h-full object-contain"
              onError={() => setError('视频解码或网络请求失败')}
            />
          )}
        </div>

        {/* Footer info */}
        <div className="px-6 py-3 bg-zinc-900/60 border-t border-zinc-800/80 flex items-center justify-between text-xs text-zinc-400">
          <span>流媒体模式: 直链直出 (Direct Play) / STRM 穿透</span>
          <span className="font-mono text-zinc-500">ID: {item.Id}</span>
        </div>
      </div>
    </div>
  );
};
