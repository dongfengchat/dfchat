import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('electronAPI', {
  platform: process.platform,
  version: process.versions.electron,

  showNotification: (payload: { title: string; body: string; conversationId?: string }) =>
    ipcRenderer.invoke('notification:show', payload) as Promise<boolean>,

  setBadge: (count: number) => ipcRenderer.invoke('badge:set', count) as Promise<void>,

  isWindowFocused: () => ipcRenderer.invoke('window:isFocused') as Promise<boolean>,

  onActivateConversation: (cb: (conversationId: string) => void) => {
    const handler = (_evt: unknown, conversationId: string) => cb(conversationId);
    ipcRenderer.on('activate-conversation', handler);
    return () => ipcRenderer.removeListener('activate-conversation', handler);
  },

  minimize: () => ipcRenderer.invoke('window:minimize') as Promise<void>,
  maximizeToggle: () => ipcRenderer.invoke('window:maximizeToggle') as Promise<void>,
  close: () => ipcRenderer.invoke('window:close') as Promise<void>,
  getPlatform: () => ipcRenderer.invoke('window:platform') as Promise<string>,

  // Open the download page in the system browser. Optionally pass the URL
  // surfaced by the manifest so future-server-side overrides still work.
  installUpdate: (payload?: { downloadUrl?: string }) =>
    ipcRenderer.invoke('update:install', payload) as Promise<void>,
  onUpdateAvailable: (cb: (info: { version: string; downloadUrl?: string; notes?: string }) => void) => {
    const h = (_e: unknown, info: { version: string; downloadUrl?: string; notes?: string }) => cb(info);
    ipcRenderer.on('update:available', h);
    return () => ipcRenderer.removeListener('update:available', h);
  },
  // Manual check-for-updates from Settings → 关于.
  checkForUpdates: () =>
    ipcRenderer.invoke('update:checkNow') as Promise<{
      current: string;
      latest?: string;
      available: boolean;
      downloadUrl?: string;
      notes?: string;
    }>,

  // Diagnostics: forward renderer errors to main, which appends them to
  // app.getPath('logs')/crash-<date>.log. Also exposes "open logs folder"
  // for the Settings page so users can grab the file when reporting bugs.
  reportError: (payload: { message: string; stack?: string; ctx?: string }) =>
    ipcRenderer.invoke('diag:reportError', payload) as Promise<void>,
  openLogsFolder: () => ipcRenderer.invoke('diag:openLogs') as Promise<void>,
});

// Encrypted local message archive. Lives in the main process; this
// surface exposes the minimal CRUD the renderer needs. All content is
// AES-256-GCM encrypted at rest, key wrapped via OS keychain (Electron
// safeStorage). See electron/archive.ts for the contract.
interface ArchivedMessage {
  id: string;
  conversationId: string;
  senderId: string;
  type: string;
  content: unknown;
  seq: number;
  mentions?: string[];
  replyTo?: string;
  isRecalled: boolean;
  editedAt?: string;
  editCount?: number;
  createdAt: string;
}
contextBridge.exposeInMainWorld('dfchatArchive', {
  // Write-through path used by the chat store on every chat.recv /
  // chat.edit / send-response.
  append: (msg: ArchivedMessage) => ipcRenderer.invoke('archive:append', msg) as Promise<void>,
  markRecalled: (messageId: string) => ipcRenderer.invoke('archive:markRecalled', messageId) as Promise<void>,
  remove: (messageId: string) => ipcRenderer.invoke('archive:remove', messageId) as Promise<void>,
  // Read path used on app start to hydrate active conversations and
  // when the user scrolls back beyond the in-memory cache.
  queryByConv: (convId: string, limit: number, beforeSeq?: number) =>
    ipcRenderer.invoke('archive:queryByConv', { convId, limit, beforeSeq }) as Promise<ArchivedMessage[]>,
  // Sync helper: the renderer asks the server for everything newer
  // than this seq, instead of refetching from zero.
  maxSeq: (convId: string) => ipcRenderer.invoke('archive:maxSeq', convId) as Promise<number>,
  // Settings → 本地归档 stats panel.
  stats: () => ipcRenderer.invoke('archive:stats') as Promise<{
    rows: number;
    earliestCreatedAt: string | null;
    latestCreatedAt: string | null;
    dbBytes: number;
  }>,
  // Export / import buttons. Both open a native dialog and return
  // { ok, count?, path?, err? }; the renderer just toasts the result.
  export: () => ipcRenderer.invoke('archive:export') as Promise<{ ok: boolean; count?: number; path?: string; err?: string }>,
  import: () => ipcRenderer.invoke('archive:import') as Promise<{ ok: boolean; count?: number; err?: string }>,
});

// Hook renderer-side unhandled errors so they get persisted automatically.
window.addEventListener('error', (e) => {
  const err = e.error as Error | undefined;
  ipcRenderer.invoke('diag:reportError', {
    message: err?.message ?? String(e.message),
    stack: err?.stack,
    ctx: 'window.onerror',
  }).catch(() => { /* ignore */ });
});
window.addEventListener('unhandledrejection', (e) => {
  const r = e.reason;
  ipcRenderer.invoke('diag:reportError', {
    message: r?.message ?? String(r),
    stack: r?.stack,
    ctx: 'unhandledrejection',
  }).catch(() => { /* ignore */ });
});
