/// <reference types="vite/client" />

// Injected by vite.config.ts (define: __APP_VERSION__) at build time;
// always matches package.json.version, which is what electron-builder
// stamps into the .app / .exe / .AppImage.
declare const __APP_VERSION__: string;

interface ImportMetaEnv {
  readonly VITE_API_BASE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

interface ElectronAPI {
  platform: string;
  version: string;
  showNotification: (p: { title: string; body: string; conversationId?: string }) => Promise<boolean>;
  setBadge: (count: number) => Promise<void>;
  isWindowFocused: () => Promise<boolean>;
  onActivateConversation: (cb: (conversationId: string) => void) => () => void;
  minimize: () => Promise<void>;
  maximizeToggle: () => Promise<void>;
  close: () => Promise<void>;
  getPlatform: () => Promise<string>;
  installUpdate: (payload?: { downloadUrl?: string }) => Promise<void>;
  onUpdateAvailable: (cb: (info: { version: string; downloadUrl?: string; notes?: string }) => void) => () => void;
  checkForUpdates: () => Promise<{
    current: string;
    latest?: string;
    available: boolean;
    downloadUrl?: string;
    notes?: string;
  }>;
  reportError: (payload: { message: string; stack?: string; ctx?: string }) => Promise<void>;
  openLogsFolder: () => Promise<void>;
}

interface Window {
  electronAPI?: ElectronAPI;
}
