import { app, BrowserWindow, dialog, ipcMain, Menu, Notification, shell, Tray, nativeImage } from 'electron';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import https from 'node:https';
import * as archive from './archive.js';

// === Crash / error logging ===========================================
// Write any uncaught error from main or renderer into a daily log file
// under app.getPath('logs'). Users can attach those when reporting bugs.
// We don't ship them anywhere automatically.
function crashLogPath(): string {
  const dir = app.getPath('logs');
  try { fs.mkdirSync(dir, { recursive: true }); } catch { /* ignore */ }
  const stamp = new Date().toISOString().slice(0, 10);
  return path.join(dir, `crash-${stamp}.log`);
}
function logCrash(source: string, err: unknown) {
  try {
    const stamp = new Date().toISOString();
    const msg = err instanceof Error ? `${err.message}\n${err.stack}` : String(err);
    fs.appendFileSync(crashLogPath(), `\n[${stamp}] ${source}\n${msg}\n`);
  } catch { /* don't crash crash-handler */ }
}
process.on('uncaughtException', (e) => logCrash('main:uncaughtException', e));
process.on('unhandledRejection', (e) => logCrash('main:unhandledRejection', e));

// Lightweight self-hosted update check: on launch and every 6 h, fetch a
// small JSON manifest, compare versions, and notify the renderer to show
// a banner with a per-OS download link. We intentionally do NOT auto-
// install — macOS Squirrel requires Apple Developer ID signing, which we
// don't have yet. After SignPath (Win) + Apple Developer (Mac) onboarding
// we can drop this in favour of electron-updater for true silent updates.
const UPDATE_MANIFEST_URL = 'https://dfchat.chat/updates/latest.json';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const VITE_DEV_SERVER_URL = process.env.VITE_DEV_SERVER_URL;
const isDev = !!VITE_DEV_SERVER_URL;

let mainWindow: BrowserWindow | null = null;
let tray: Tray | null = null;

function showOrCreateWindow() {
  if (mainWindow) {
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
    return;
  }

  const isMac = process.platform === 'darwin';
  const isWin = process.platform === 'win32';

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 960,
    minHeight: 640,
    backgroundColor: '#0b0d11',
    title: '东风快信',
    // Frameless on macOS: keep traffic lights, hide title bar so content can
    // flow to the very top.
    titleBarStyle: isMac ? 'hiddenInset' : isWin ? 'hidden' : 'default',
    trafficLightPosition: isMac ? { x: 14, y: 12 } : undefined,
    // Windows-style native system buttons drawn at top right.
    titleBarOverlay: isWin
      ? { color: '#15171d', symbolColor: '#cbd0d8', height: 36 }
      : undefined,
    webPreferences: {
      preload: path.join(__dirname, 'preload.mjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });

  if (isDev) {
    mainWindow.loadURL(VITE_DEV_SERVER_URL!);
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  } else {
    mainWindow.loadFile(path.join(__dirname, '../dist/index.html'));
  }

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

function setupIpc() {
  ipcMain.handle(
    'notification:show',
    (_evt, payload: { title: string; body: string; conversationId?: string }): boolean => {
      if (!Notification.isSupported()) return false;
      const n = new Notification({ title: payload.title, body: payload.body, silent: false });
      n.on('click', () => {
        showOrCreateWindow();
        if (payload.conversationId && mainWindow) {
          mainWindow.webContents.send('activate-conversation', payload.conversationId);
        }
      });
      n.show();
      return true;
    },
  );

  ipcMain.handle('badge:set', (_evt, count: number) => {
    const safe = Math.max(0, Math.floor(count || 0));
    if (process.platform === 'darwin') {
      if (app.dock) {
        app.dock.setBadge(safe > 0 ? (safe > 99 ? '99+' : String(safe)) : '');
      }
    } else if (process.platform === 'win32') {
      if (mainWindow) {
        if (safe > 0) {
          const img = nativeImage.createFromBuffer(redDotPngBuffer());
          mainWindow.setOverlayIcon(img, `${safe} unread`);
        } else {
          mainWindow.setOverlayIcon(null, '');
        }
      }
    }
  });

  ipcMain.handle('window:isFocused', () => {
    return !!mainWindow && mainWindow.isFocused() && mainWindow.isVisible();
  });

  // Window controls invoked from the custom title bar.
  ipcMain.handle('window:minimize', () => mainWindow?.minimize());
  ipcMain.handle('window:maximizeToggle', () => {
    if (!mainWindow) return;
    if (mainWindow.isMaximized()) mainWindow.unmaximize();
    else mainWindow.maximize();
  });
  ipcMain.handle('window:close', () => mainWindow?.close());
  ipcMain.handle('window:platform', () => process.platform);

  // Renderer-side errors land here via preload; same log file as main crashes.
  ipcMain.handle('diag:reportError', (_e, payload: { message: string; stack?: string; ctx?: string }) => {
    logCrash(`renderer:${payload.ctx || 'error'}`, new Error(`${payload.message}\n${payload.stack || ''}`));
  });
  // Settings → 关于 → 打开日志文件夹.
  ipcMain.handle('diag:openLogs', () => {
    const dir = app.getPath('logs');
    try { fs.mkdirSync(dir, { recursive: true }); } catch { /* ignore */ }
    shell.openPath(dir);
  });

  // Renderer asks us to "install" — without code signing we can't swap the
  // app in place, so we just open the download page in the user's browser.
  ipcMain.handle('update:install', (_e, payload?: { downloadUrl?: string }) => {
    const url = payload?.downloadUrl || 'https://dfchat.chat/#download';
    shell.openExternal(url);
  });

  // Manual "check now" trigger from Settings → 关于. Returns the result
  // synchronously so the UI can show "已是最新" / "发现新版本 vX.Y.Z".
  ipcMain.handle('update:checkNow', async (): Promise<{
    current: string;
    latest?: string;
    available: boolean;
    downloadUrl?: string;
    notes?: string;
  }> => {
    const current = app.getVersion();
    const m = await fetchManifest();
    if (!m?.version) return { current, available: false };
    const available = compareSemver(m.version, current) > 0;
    return {
      current,
      latest: m.version,
      available,
      downloadUrl: available ? pickDownloadUrl(m) : undefined,
      notes: m.notes,
    };
  });

  // === Encrypted local archive IPC =================================
  // All writes are fire-and-forget from the renderer's perspective —
  // the renderer keeps its in-memory store as the read-source of
  // truth and the archive is a write-through cache. Errors get
  // logged via diag instead of bubbling up, because a write
  // failure shouldn't block the user from chatting.
  ipcMain.handle('archive:append', (_e, msg: archive.ArchivedMessage) => {
    try { archive.appendMessage(msg); }
    catch (err) { logCrash('archive:append', err); }
  });
  ipcMain.handle('archive:markRecalled', (_e, messageId: string) => {
    try { archive.markRecalled(messageId); }
    catch (err) { logCrash('archive:markRecalled', err); }
  });
  ipcMain.handle('archive:remove', (_e, messageId: string) => {
    try { archive.remove(messageId); }
    catch (err) { logCrash('archive:remove', err); }
  });
  ipcMain.handle('archive:queryByConv', (_e, p: { convId: string; limit: number; beforeSeq?: number }) => {
    try { return archive.queryByConv(p.convId, p.limit, p.beforeSeq); }
    catch (err) { logCrash('archive:queryByConv', err); return []; }
  });
  ipcMain.handle('archive:maxSeq', (_e, convId: string) => {
    try { return archive.maxSeq(convId); }
    catch (err) { logCrash('archive:maxSeq', err); return 0; }
  });
  ipcMain.handle('archive:stats', () => {
    try { return archive.stats(); }
    catch (err) { logCrash('archive:stats', err); return { rows: 0, earliestCreatedAt: null, latestCreatedAt: null, dbBytes: 0 }; }
  });
  // Export prompts the user for a save location and writes a JSON
  // dump of every archived message. The exported file is plaintext
  // by design — the whole point is portability.
  ipcMain.handle('archive:export', async (): Promise<{ ok: boolean; count?: number; path?: string; err?: string }> => {
    if (!mainWindow) return { ok: false, err: 'no window' };
    const stamp = new Date().toISOString().slice(0, 10);
    const res = await dialog.showSaveDialog(mainWindow, {
      title: '导出聊天记录',
      defaultPath: `dfchat-archive-${stamp}.json`,
      filters: [{ name: 'JSON', extensions: ['json'] }],
    });
    if (res.canceled || !res.filePath) return { ok: false };
    try {
      const count = archive.exportAll(res.filePath);
      return { ok: true, count, path: res.filePath };
    } catch (err) {
      logCrash('archive:export', err);
      return { ok: false, err: String(err) };
    }
  });
  ipcMain.handle('archive:import', async (): Promise<{ ok: boolean; count?: number; err?: string }> => {
    if (!mainWindow) return { ok: false, err: 'no window' };
    const res = await dialog.showOpenDialog(mainWindow, {
      title: '导入聊天记录',
      filters: [{ name: 'JSON', extensions: ['json'] }],
      properties: ['openFile'],
    });
    if (res.canceled || !res.filePaths[0]) return { ok: false };
    try {
      const count = archive.importMessages(res.filePaths[0]);
      return { ok: true, count };
    } catch (err) {
      logCrash('archive:import', err);
      return { ok: false, err: String(err) };
    }
  });
}

function redDotPngBuffer(): Buffer {
  const base64 =
    'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAS0lEQVQ4T2NkYGD4z0AEYBxV' +
    'SCgAQwGDB8H/DAYjE/2///8/IzPDfwYGsBwTw3+G/0xAAcZRgwbCABaG/wxMQxsCNGgIBgAA' +
    'GZkB/wEYahcAAAAASUVORK5CYII=';
  return Buffer.from(base64, 'base64');
}

function setupTray() {
  const icon = nativeImage.createFromBuffer(redDotPngBuffer()).resize({ width: 16, height: 16 });
  if (process.platform === 'darwin') icon.setTemplateImage(true);
  tray = new Tray(icon);
  tray.setToolTip('东风快信');
  tray.on('click', () => showOrCreateWindow());

  const menu = Menu.buildFromTemplate([
    { label: '打开东风快信', click: () => showOrCreateWindow() },
    { type: 'separator' },
    { label: '退出', click: () => app.quit() },
  ]);
  tray.setContextMenu(menu);
}

interface UpdateManifest {
  version: string;
  /** Optional per-platform direct links keyed by `${platform}-${arch}`,
   *  e.g. "darwin-arm64" / "darwin-x64" / "win32-x64" / "linux-x64".
   *  Falls back to `downloadUrl` if the current platform isn't listed. */
  downloads?: Record<string, string>;
  /** Catch-all download page (current behaviour). */
  downloadUrl?: string;
  notes?: string;
}

function pickDownloadUrl(m: UpdateManifest): string {
  const key = `${process.platform}-${process.arch}`;
  return m.downloads?.[key] || m.downloadUrl || 'https://dfchat.chat/#download';
}

function fetchManifest(): Promise<UpdateManifest | null> {
  return new Promise((resolve) => {
    const req = https.get(UPDATE_MANIFEST_URL, { timeout: 8000 }, (res) => {
      if (res.statusCode !== 200) { res.resume(); return resolve(null); }
      let data = '';
      res.setEncoding('utf8');
      res.on('data', (c) => { data += c; });
      res.on('end', () => {
        try { resolve(JSON.parse(data) as UpdateManifest); } catch { resolve(null); }
      });
    });
    req.on('error', () => resolve(null));
    req.on('timeout', () => { req.destroy(); resolve(null); });
  });
}

// Returns 1 if a>b, -1 if a<b, 0 if equal. Treats missing parts as 0.
function compareSemver(a: string, b: string): number {
  const pa = a.split('.').map((n) => parseInt(n, 10) || 0);
  const pb = b.split('.').map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] || 0; const y = pb[i] || 0;
    if (x > y) return 1; if (x < y) return -1;
  }
  return 0;
}

function setupAutoUpdater() {
  if (isDev) return;
  const current = app.getVersion();
  let lastCheckAt = 0;
  const check = async () => {
    lastCheckAt = Date.now();
    const m = await fetchManifest();
    if (!m?.version) return;
    if (compareSemver(m.version, current) > 0) {
      mainWindow?.webContents.send('update:available', {
        version: m.version,
        downloadUrl: pickDownloadUrl(m),
        notes: m.notes || '',
      });
    }
  };
  // First check 5s after start; recheck every 6h while the app is open.
  setTimeout(check, 5000);
  setInterval(check, 6 * 60 * 60 * 1000);
  // Also recheck when the user re-focuses the window after being away
  // for 30+ minutes — catches "came back from lunch, the new version
  // just shipped" case without polling every minute.
  app.on('browser-window-focus', () => {
    if (Date.now() - lastCheckAt > 30 * 60 * 1000) {
      check();
    }
  });
}

app.whenReady().then(() => {
  // Open the encrypted local archive first — renderer code expects
  // window.dfchatArchive to be ready by the time it boots.
  try {
    archive.open();
  } catch (err) {
    logCrash('archive:open', err);
  }
  setupIpc();
  setupTray();
  showOrCreateWindow();
  setupAutoUpdater();
});

app.on('window-all-closed', () => {
  try { archive.close(); } catch { /* ignore */ }
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  showOrCreateWindow();
});

app.on('before-quit', () => {
  // Final flush + zero the in-memory key. archive.close is idempotent.
  try { archive.close(); } catch { /* ignore */ }
});
