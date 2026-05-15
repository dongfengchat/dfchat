import { useEffect, useState } from 'react';
import { Download, X } from 'lucide-react';

/**
 * Lightweight update notifier. The main process polls a JSON manifest on
 * dfchat.chat once at startup and every 6h; when it spots a newer version
 * it sends `update:available` and we render this banner.
 *
 * "立即下载" hands off to the system browser — we can't auto-swap the app
 * without an Apple Developer ID code signature. Dismiss is per-session.
 */
export default function UpdateBanner() {
  const [info, setInfo] = useState<{ version: string; downloadUrl?: string } | null>(null);
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    if (!window.electronAPI) return;
    const off = window.electronAPI.onUpdateAvailable((i) => {
      setInfo({ version: i.version, downloadUrl: i.downloadUrl });
      setDismissed(false);
    });
    return () => off();
  }, []);

  if (!info || dismissed) return null;

  return (
    <div className="fixed bottom-5 left-1/2 -translate-x-1/2 z-40 anim-slide">
      <div className="card shadow-pop px-4 py-2.5 flex items-center gap-3 max-w-md">
        <Download size={18} className="text-brand-300 shrink-0" />
        <div className="text-sm text-ink-1 min-w-0">
          新版本 <span className="text-brand-300 font-medium">v{info.version}</span> 已发布
        </div>
        <button
          onClick={() => window.electronAPI?.installUpdate({ downloadUrl: info.downloadUrl })}
          className="btn-primary shrink-0 py-1.5"
        >
          立即下载
        </button>
        <button
          onClick={() => setDismissed(true)}
          className="btn-icon w-7 h-7"
          aria-label="稍后"
        >
          <X size={14} />
        </button>
      </div>
    </div>
  );
}
