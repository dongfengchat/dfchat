import { useEffect, useState } from 'react';
import { Download, Sparkles, X } from 'lucide-react';

/**
 * Prominent top-of-window update banner. The main process polls
 * dfchat.chat/updates/latest.json at startup and every 6 h; when it
 * finds a newer version it sends `update:available` and we mount this
 * banner above the rest of the UI.
 *
 * Design notes:
 *   - Lives at the very top of the viewport (fixed) so the user sees
 *     it the moment the app paints. The old version was a bottom-toast,
 *     which testers consistently overlooked.
 *   - Shows the release notes (truncated) so users know what's new and
 *     can make an informed "now vs later" call.
 *   - Dismissal is per-version in localStorage: dismiss v0.1.23 once,
 *     don't see it again, but v0.1.24 still notifies. Stops the "I
 *     dismissed it last week, never saw v0.1.30" footgun.
 *   - "立即下载" opens the native browser to the per-platform installer.
 *     Without an Apple Developer ID + signed Squirrel feed we can't
 *     auto-swap the app; this is the best UX until we can.
 */
const DISMISS_KEY = 'dfchat:dismissed-update';

export default function UpdateBanner() {
  const [info, setInfo] = useState<{ version: string; downloadUrl?: string; notes?: string } | null>(null);
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    if (!window.electronAPI) return;
    const off = window.electronAPI.onUpdateAvailable((i) => {
      // Was this exact version already dismissed?
      const skip = localStorage.getItem(DISMISS_KEY) === i.version;
      setInfo({ version: i.version, downloadUrl: i.downloadUrl, notes: i.notes });
      setDismissed(skip);
    });
    return () => off();
  }, []);

  if (!info || dismissed) return null;

  function onDismiss() {
    localStorage.setItem(DISMISS_KEY, info!.version);
    setDismissed(true);
  }

  function onDownload() {
    window.electronAPI?.installUpdate({ downloadUrl: info!.downloadUrl });
  }

  return (
    <div className="fixed top-0 left-0 right-0 z-[60] anim-slide">
      <div
        className="px-4 py-2.5 flex items-center gap-3 shadow-md border-b border-brand-500/50"
        style={{
          background: 'linear-gradient(90deg, rgba(72,105,247,0.18) 0%, rgba(72,105,247,0.10) 100%)',
        }}
      >
        <div className="flex items-center gap-2 shrink-0">
          <Sparkles size={16} className="text-brand-300" />
          <span className="text-sm font-medium text-ink-1">
            新版本 <span className="font-mono text-brand-300">v{info.version}</span> 已发布
          </span>
        </div>
        {info.notes ? (
          <div className="text-xs text-ink-3 min-w-0 truncate flex-1" title={info.notes}>
            · {info.notes}
          </div>
        ) : (
          <div className="flex-1" />
        )}
        <button
          onClick={onDownload}
          className="btn-primary py-1 px-3 text-xs shrink-0"
        >
          <Download size={12} /> 立即下载
        </button>
        <button
          onClick={onDismiss}
          className="btn-icon w-7 h-7 shrink-0"
          aria-label="本版本不再提示"
          title="本版本不再提示（下一版仍会通知）"
        >
          <X size={14} />
        </button>
      </div>
    </div>
  );
}
