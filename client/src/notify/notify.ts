import type { ChatMessage } from '@/types';
import { toast } from '@/components/ui/Toast';
import { isSoundEnabled, playMentionChime, playNotifyChime } from './sound';

async function isFocusedOnConv(convId: string, activeConvId: string | null): Promise<boolean> {
  if (!window.electronAPI) {
    return document.visibilityState === 'visible' && activeConvId === convId;
  }
  const focused = await window.electronAPI.isWindowFocused();
  return focused && activeConvId === convId;
}

async function isAppFocused(): Promise<boolean> {
  if (!window.electronAPI) return document.visibilityState === 'visible';
  return window.electronAPI.isWindowFocused();
}

function previewText(msg: ChatMessage): string {
  if (msg.type === 'text' && typeof msg.content?.text === 'string') {
    const t = msg.content.text as string;
    return t.length > 80 ? `${t.slice(0, 80)}…` : t;
  }
  if (msg.type === 'image') return '[图片]';
  if (msg.type === 'file') return '[文件]';
  return '[消息]';
}

// Debounce: same conv getting multiple messages in 5 s window collapses
// into a single banner so we don't carpet-bomb the user with toasts.
const pendingAt: Map<string, number> = new Map();
const DEBOUNCE_MS = 5000;

interface NotifyMeta {
  senderName: string;
  conversationTitle: string;
  activeConvId: string | null;
  isMention: boolean;
}

export async function maybeNotify(msg: ChatMessage, meta: NotifyMeta): Promise<void> {
  // 1) If the user is already looking at this conv with the window
  // focused, do nothing — they see it in the conversation already.
  if (await isFocusedOnConv(msg.conversationId, meta.activeConvId)) return;

  // 2) Audible chime — mentions get a brighter two-note chime.
  if (isSoundEnabled()) {
    if (meta.isMention) playMentionChime();
    else playNotifyChime();
  }

  const isGroupConv = msg.conversationId.startsWith('g_') || msg.conversationId.startsWith('c_');
  const title = isGroupConv
    ? `${meta.conversationTitle} · ${meta.senderName}`
    : (meta.senderName || meta.conversationTitle);
  const body = previewText(msg);

  // 3) Channel: app-focused → in-app toast; app-bg → OS desktop notification.
  const focused = await isAppFocused();
  if (focused) {
    // Debounce burst messages from the same conv.
    const lastAt = pendingAt.get(msg.conversationId) ?? 0;
    if (Date.now() - lastAt < DEBOUNCE_MS) return;
    pendingAt.set(msg.conversationId, Date.now());

    toast(
      meta.isMention ? `🔔 ${title} 提到了你: ${body}` : `${title}: ${body}`,
      'info',
    );
    return;
  }

  // 4) App in background — OS-level desktop notification.
  if (window.electronAPI) {
    await window.electronAPI.showNotification({
      title,
      body,
      conversationId: msg.conversationId,
    });
    return;
  }
  // Browser fallback.
  if ('Notification' in window) {
    if (Notification.permission === 'default') {
      try { await Notification.requestPermission(); } catch { /* ignore */ }
    }
    if (Notification.permission === 'granted') {
      new Notification(title, { body });
    }
  }
}

export async function setBadge(count: number): Promise<void> {
  if (window.electronAPI) {
    await window.electronAPI.setBadge(count);
  } else if ('setAppBadge' in navigator) {
    try {
      if (count > 0) await (navigator as any).setAppBadge(count);
      else await (navigator as any).clearAppBadge();
    } catch {
      // ignore
    }
  }
}
