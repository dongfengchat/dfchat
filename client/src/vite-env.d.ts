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

// Encrypted local message archive exposed by the Electron preload
// script. Only available when the renderer is hosted inside Electron;
// the browser-mode (vite dev with VITE_API_BASE) build sees undefined
// and falls back to the in-memory store + server only.
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
interface DFChatArchive {
  append: (msg: ArchivedMessage) => Promise<void>;
  markRecalled: (messageId: string) => Promise<void>;
  remove: (messageId: string) => Promise<void>;
  queryByConv: (convId: string, limit: number, beforeSeq?: number) => Promise<ArchivedMessage[]>;
  maxSeq: (convId: string) => Promise<number>;
  stats: () => Promise<{
    rows: number;
    earliestCreatedAt: string | null;
    latestCreatedAt: string | null;
    dbBytes: number;
  }>;
  export: () => Promise<{ ok: boolean; count?: number; path?: string; err?: string }>;
  import: () => Promise<{ ok: boolean; count?: number; err?: string }>;
}

interface Window {
  electronAPI?: ElectronAPI;
  dfchatArchive?: DFChatArchive;
}
