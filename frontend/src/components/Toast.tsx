import React, { useEffect, useState } from 'react';
import { onToast } from '../api/client';
import { CheckCircle2, AlertCircle, Info, X } from 'lucide-react';

interface ToastItem {
  id: string;
  msg: string;
  type: 'info' | 'success' | 'error' | 'warning';
}

export const ToastContainer: React.FC = () => {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  useEffect(() => {
    const unsub = onToast((msg, type = 'info') => {
      const id = Math.random().toString(36).slice(2, 9);
      setToasts((prev) => [...prev, { id, msg, type }]);
      setTimeout(() => {
        setToasts((prev) => prev.filter((t) => t.id !== id));
      }, 4000);
    });
    return () => {
      unsub();
    };
  }, []);

  const removeToast = (id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  };

  if (toasts.length === 0) return null;

  return (
    <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-2 max-w-md pointer-events-none">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`pointer-events-auto flex items-center gap-3 px-4 py-3 rounded-xl shadow-2xl backdrop-blur-md border text-sm transition-all duration-300 animate-in slide-in-from-bottom-2 ${
            t.type === 'success'
              ? 'bg-emerald-950/90 border-emerald-500/40 text-emerald-200'
              : t.type === 'error'
              ? 'bg-rose-950/90 border-rose-500/40 text-rose-200'
              : t.type === 'warning'
              ? 'bg-amber-950/90 border-amber-500/40 text-amber-200'
              : 'bg-zinc-900/90 border-zinc-700/60 text-zinc-200'
          }`}
        >
          {t.type === 'success' && <CheckCircle2 className="w-5 h-5 text-emerald-400 shrink-0" />}
          {t.type === 'error' && <AlertCircle className="w-5 h-5 text-rose-400 shrink-0" />}
          {t.type === 'warning' && <AlertCircle className="w-5 h-5 text-amber-400 shrink-0" />}
          {t.type === 'info' && <Info className="w-5 h-5 text-sky-400 shrink-0" />}
          <span className="flex-1 font-medium">{t.msg}</span>
          <button
            onClick={() => removeToast(t.id)}
            className="text-zinc-400 hover:text-zinc-100 p-1 rounded-lg hover:bg-white/10 transition"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      ))}
    </div>
  );
};
