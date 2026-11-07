import React, { useState, useEffect } from 'react';
import { Folder, FolderPlus, ChevronRight, ArrowUp, Check, X, HardDrive } from 'lucide-react';
import { fetchDirectories, notifyToast } from '../api/client';

interface DirectoryPickerModalProps {
  isOpen: boolean;
  initialPath?: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}

export const DirectoryPickerModal: React.FC<DirectoryPickerModalProps> = ({
  isOpen,
  initialPath = '/media/strm',
  onSelect,
  onClose,
}) => {
  const [currentPath, setCurrentPath] = useState<string>(initialPath || '/media/strm');
  const [directories, setDirectories] = useState<string[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [manualPath, setManualPath] = useState<string>(initialPath || '/media/strm');

  const loadDirs = async (p: string) => {
    setLoading(true);
    try {
      const res = await fetchDirectories(p);
      setCurrentPath(res.Path || p);
      setManualPath(res.Path || p);
      setDirectories(res.Directories || []);
    } catch (err: any) {
      notifyToast(err.message || '加载目录失败', 'error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (isOpen) {
      loadDirs(initialPath || '/media/strm');
    }
  }, [isOpen, initialPath]);

  if (!isOpen) return null;

  const navigateUp = () => {
    if (currentPath === '/' || currentPath === '') return;
    const parts = currentPath.split('/').filter(Boolean);
    parts.pop();
    const upPath = '/' + parts.join('/');
    loadDirs(upPath);
  };

  const handleSelect = (p: string) => {
    onSelect(p);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-4 animate-in fade-in">
      <div className="w-full max-w-2xl bg-zinc-900 border border-zinc-800 rounded-2xl shadow-2xl flex flex-col max-h-[85vh] overflow-hidden">
        {/* Header */}
        <div className="px-6 py-4 border-b border-zinc-800 flex items-center justify-between bg-zinc-950/60">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-emerald-950 border border-emerald-500/30 flex items-center justify-center">
              <Folder className="w-4 h-4 text-emerald-400" />
            </div>
            <div>
              <h3 className="text-base font-bold text-zinc-100">选择服务器媒体目录</h3>
              <p className="text-xs text-zinc-500">点选进入子目录，或直接选择当前所在目录</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-lg bg-zinc-900 border border-zinc-800 hover:bg-zinc-800 text-zinc-400 hover:text-white transition"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Quick Shortcuts */}
        <div className="px-6 py-3 border-b border-zinc-800/60 bg-zinc-900/40 flex items-center gap-2 overflow-x-auto text-xs">
          <span className="text-zinc-500 shrink-0 flex items-center gap-1">
            <HardDrive className="w-3.5 h-3.5" /> 常用目录:
          </span>
          {['/media', '/media/strm', '/media/strm/电影', '/media/strm/电视剧'].map((p) => (
            <button
              key={p}
              onClick={() => loadDirs(p)}
              className="px-2.5 py-1 rounded-lg bg-zinc-800/80 hover:bg-zinc-700/80 text-zinc-300 hover:text-white transition shrink-0 font-mono text-[11px]"
            >
              {p}
            </button>
          ))}
        </div>

        {/* Current Path Bar & Parent Navigation */}
        <div className="px-6 py-3 bg-zinc-950/40 border-b border-zinc-800/60 flex items-center gap-2">
          <button
            onClick={navigateUp}
            disabled={currentPath === '/' || currentPath === '' || loading}
            className="flex items-center gap-1 px-3 py-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium disabled:opacity-40 transition shrink-0"
          >
            <ArrowUp className="w-3.5 h-3.5" /> 上一级
          </button>

          <div className="flex-1 px-3 py-1.5 rounded-lg bg-zinc-950 border border-zinc-800 text-xs font-mono text-emerald-400 truncate">
            {currentPath || '/'}
          </div>

          <button
            onClick={() => handleSelect(currentPath)}
            className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold shadow-md shadow-emerald-600/20 transition shrink-0"
          >
            <Check className="w-3.5 h-3.5" /> 选择此目录
          </button>
        </div>

        {/* Directories List */}
        <div className="flex-1 p-4 overflow-y-auto space-y-1.5">
          {loading ? (
            <div className="py-12 text-center text-zinc-500 text-sm">正在加载目录列表...</div>
          ) : directories.length === 0 ? (
            <div className="py-12 text-center text-zinc-500 text-sm flex flex-col items-center gap-2">
              <FolderPlus className="w-8 h-8 opacity-40 text-zinc-400" />
              <span>当前路径下无子目录，您可直接点击上方「选择此目录」</span>
            </div>
          ) : (
            directories.map((dirName) => {
              const full = currentPath === '/' ? `/${dirName}` : `${currentPath}/${dirName}`;
              return (
                <div
                  key={dirName}
                  className="flex items-center justify-between p-2.5 rounded-xl bg-zinc-950/50 hover:bg-zinc-800/60 border border-zinc-800/60 hover:border-zinc-700/80 transition group"
                >
                  <div
                    className="flex items-center gap-3 cursor-pointer flex-1 min-w-0"
                    onClick={() => loadDirs(full)}
                  >
                    <Folder className="w-4 h-4 text-emerald-400 shrink-0" />
                    <span className="text-sm font-medium text-zinc-200 truncate group-hover:text-emerald-300">
                      {dirName}
                    </span>
                  </div>

                  <div className="flex items-center gap-1.5 shrink-0">
                    <button
                      onClick={() => loadDirs(full)}
                      className="px-2.5 py-1 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-zinc-300 text-xs flex items-center gap-1 transition"
                    >
                      进入 <ChevronRight className="w-3 h-3" />
                    </button>
                    <button
                      onClick={() => handleSelect(full)}
                      className="px-2.5 py-1 rounded-lg bg-emerald-950 hover:bg-emerald-900 border border-emerald-500/30 text-emerald-300 text-xs flex items-center gap-1 transition"
                    >
                      <Check className="w-3 h-3" /> 选中
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>

        {/* Footer: Manual Path Input */}
        <div className="p-4 border-t border-zinc-800 bg-zinc-950/70 flex items-center gap-3">
          <input
            type="text"
            value={manualPath}
            onChange={(e) => setManualPath(e.target.value)}
            placeholder="也可手动输入或粘贴服务器完整路径..."
            className="flex-1 px-3 py-2 rounded-xl bg-zinc-900 border border-zinc-800 text-xs font-mono text-zinc-200 focus:outline-none focus:border-emerald-500 transition"
          />
          <button
            onClick={() => {
              if (manualPath.trim()) handleSelect(manualPath.trim());
            }}
            className="px-4 py-2 rounded-xl bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs font-medium transition shrink-0"
          >
            采用输入路径
          </button>
        </div>
      </div>
    </div>
  );
};
