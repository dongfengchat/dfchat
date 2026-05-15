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

  // Diagnostics: forward renderer errors to main, which appends them to
  // app.getPath('logs')/crash-<date>.log. Also exposes "open logs folder"
  // for the Settings page so users can grab the file when reporting bugs.
  reportError: (payload: { message: string; stack?: string; ctx?: string }) =>
    ipcRenderer.invoke('diag:reportError', payload) as Promise<void>,
  openLogsFolder: () => ipcRenderer.invoke('diag:openLogs') as Promise<void>,
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
