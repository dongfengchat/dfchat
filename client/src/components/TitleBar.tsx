import { useEffect, useState } from 'react';
import { Minus, Square, X } from 'lucide-react';

// Detect Electron + platform synchronously from navigator.userAgent so the
// layout is correct on the very first paint (before any IPC round-trip).
// Falls back to electronAPI.getPlatform() afterwards as a safety net.
function detectPlatform() {
  if (typeof navigator === 'undefined') {
    return { isElectron: false, isMac: false, isWin: false };
  }
  const ua = navigator.userAgent;
  return {
    isElectron: /Electron/i.test(ua),
    isMac: /Mac OS X|Macintosh/i.test(ua),
    isWin: /Windows/i.test(ua),
  };
}

export default function TitleBar({ title = '东风快信' }: { title?: string }) {
  const initial = detectPlatform();
  const [isElectron] = useState(initial.isElectron);
  const [isMac, setIsMac] = useState(initial.isMac);
  const [isWin, setIsWin] = useState(initial.isWin);

  useEffect(() => {
    window.electronAPI?.getPlatform().then((p) => {
      setIsMac(p === 'darwin');
      setIsWin(p === 'win32');
    });
  }, []);

  if (!isElectron) return null;

  return (
    <div
      className="h-9 bg-bg-2 border-b border-bg-5/30 flex items-center select-none shrink-0"
      style={{
        WebkitAppRegion: 'drag',
        paddingLeft: isMac ? 78 : 12,
        paddingRight: isWin ? 138 : 12,
      } as React.CSSProperties}
    >
      <div className="flex-1 text-center text-xs text-ink-3 font-medium truncate">
        {title}
      </div>
      {/* Linux / generic frameless: draw our own controls. macOS uses native, Windows uses titleBarOverlay. */}
      {!isMac && !isWin && (
        <div className="flex items-center gap-1" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
          <button
            onClick={() => window.electronAPI!.minimize()}
            className="w-9 h-9 flex items-center justify-center hover:bg-bg-3 text-ink-3"
            title="最小化"
          >
            <Minus size={14} />
          </button>
          <button
            onClick={() => window.electronAPI!.maximizeToggle()}
            className="w-9 h-9 flex items-center justify-center hover:bg-bg-3 text-ink-3"
            title="最大化"
          >
            <Square size={12} />
          </button>
          <button
            onClick={() => window.electronAPI!.close()}
            className="w-9 h-9 flex items-center justify-center hover:bg-accent-red text-ink-3 hover:text-white"
            title="关闭"
          >
            <X size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
