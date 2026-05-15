import type { ChatMessage } from '@/types';

// Lightweight async getter to avoid notifying when the window is focused
// and the user is already looking at the conversation.
async function shouldNotify(convId: string, activeConvId: string | null): Promise<boolean> {
  if (!window.electronAPI) {
    // Browser/fallback path — only notify when document is hidden.
    return document.visibilityState !== 'visible' || activeConvId !== convId;
  }
  const focused = await window.electronAPI.isWindowFocused();
  if (focused && activeConvId === convId) return false;
  return true;
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

export async function maybeNotify(
  msg: ChatMessage,
  meta: { senderName: string; conversationTitle: string; activeConvId: string | null },
): Promise<void> {
  if (!(await shouldNotify(msg.conversationId, meta.activeConvId))) return;

  const title = msg.conversationId.startsWith('g_')
    ? `${meta.conversationTitle} · ${meta.senderName}`
    : meta.senderName || meta.conversationTitle;
  const body = previewText(msg);

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
      // Chrome/Edge PWA API
      if (count > 0) await (navigator as any).setAppBadge(count);
      else await (navigator as any).clearAppBadge();
    } catch {
      // ignore
    }
  }
}
