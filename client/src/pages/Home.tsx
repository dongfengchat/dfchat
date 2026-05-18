import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  channelConvId,
  fetchMe,
  getGroupNotifyMode,
  listFriends,
  listGroups,
  listPins,
  privateConvId,
} from '@/api/client';
import { useUserStore } from '@/store/userStore';
import { useChatStore, type ChatTarget } from '@/store/chatStore';
import { wsClient } from '@/ws/client';
import { syncAllConversations } from '@/sync/engine';
import { useSeqStore } from '@/sync/seqStore';
import { maybeNotify, setBadge } from '@/notify/notify';
import { toast } from '@/components/ui/Toast';
import { useCallStore, handleIncomingCallEvent } from '@/call/store';
import type { ChatMessage, Pin as PinType, ReactionCount as ReactionCountType } from '@/types';
import FriendSidebar from '@/components/FriendSidebar';
import ChatView from '@/components/ChatView';
import CallOverlay from '@/components/CallOverlay';
import TitleBar from '@/components/TitleBar';
import SearchModal from '@/components/SearchModal';

export default function Home() {
  const navigate = useNavigate();
  const { user, accessToken, setSession, clear } = useUserStore();
  const setFriends = useChatStore((s) => s.setFriends);
  const setGroups = useChatStore((s) => s.setGroups);
  const appendMessage = useChatStore((s) => s.appendMessage);
  const replaceMessage = useChatStore((s) => s.replaceMessage);
  const applyReactionUpdate = useChatStore((s) => s.applyReactionUpdate);
  const addPin = useChatStore((s) => s.addPin);
  const removePin = useChatStore((s) => s.removePin);
  const setPeerRead = useChatStore((s) => s.setPeerRead);
  const hydrateFromCache = useChatStore((s) => s.hydrateFromCache);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearchOpen(true);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    if (!accessToken) {
      navigate('/login', { replace: true });
      return;
    }

    hydrateFromCache();

    let cancelled = false;
    (async () => {
      try {
        const [me, friends, groups] = await Promise.all([fetchMe(), listFriends(), listGroups()]);
        if (cancelled) return;
        setSession(me, accessToken);
        setFriends(friends);
        setGroups(groups);
        // Hydrate per-group notify mode (so muted groups stop popping
        // toasts immediately, without waiting for the user to open
        // MembersPanel). Best-effort: a failure just leaves mode at
        // default 0 (all messages), matching server fallback.
        const { setGroupNotifyMode } = useChatStore.getState();
        await Promise.all(
          groups.map(async (g) => {
            try {
              const m = await getGroupNotifyMode(g.id);
              setGroupNotifyMode(g.id, (m as 0 | 1 | 2) ?? 0);
            } catch { /* keep default */ }
          }),
        );
      } catch (err: any) {
        if (!cancelled) setError(err.message ?? '加载失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    wsClient.connect(accessToken);

    const offMsg = wsClient.on((ev) => {
      if (ev.type === 'chat.recv') {
        const msg = ev.payload as ChatMessage;
        appendMessage(msg);

        const meId = useUserStore.getState().user?.id;
        if (msg.senderId === meId) return;
        // Honor "do not disturb" — muted conversation skips banner/sound.
        const convMuted = useChatStore.getState().mutedConvs.has(msg.conversationId);

        const { friends, groups, channelsByGroup, activeTarget, groupNotifyModes } = useChatStore.getState();

        // Decide whether to fire toast/sound/desktop notification. Even if
        // we skip the notification, we still want the sidebar unread badge
        // to update so the user can see "X conversations have new content".
        let notifyAllowed = !convMuted;
        let owningGroupId: string | null = null;
        if (msg.conversationId.startsWith('g_')) {
          owningGroupId = msg.conversationId.slice(2);
        } else if (msg.conversationId.startsWith('c_')) {
          const channelId = msg.conversationId.slice(2);
          for (const [gid, chs] of Object.entries(channelsByGroup)) {
            if (chs.some((c) => c.id === channelId)) { owningGroupId = gid; break; }
          }
        }
        if (notifyAllowed && owningGroupId && meId) {
          const mode = groupNotifyModes[owningGroupId] ?? 0;
          if (mode === 2) notifyAllowed = false;
          if (mode === 1) {
            const mentioned = (msg.mentions ?? []).map(String).includes(String(meId));
            if (!mentioned) notifyAllowed = false;
          }
        }
        let senderName = `用户 ${msg.senderId}`;
        let conversationTitle = senderName;

        if (msg.conversationId.startsWith('c_')) {
          const channelId = msg.conversationId.slice(2);
          for (const [gid, chs] of Object.entries(channelsByGroup)) {
            const ch = chs.find((c) => c.id === channelId);
            if (ch) {
              const g = groups.find((x) => x.id === gid);
              conversationTitle = g ? `${g.name} · #${ch.name}` : `#${ch.name}`;
              break;
            }
          }
          const friend = friends.find((f) => f.id === msg.senderId);
          if (friend) senderName = friend.nickname || friend.username;
        } else if (msg.conversationId.startsWith('p_')) {
          const f = friends.find((x) => x.id === msg.senderId);
          if (f) {
            senderName = f.nickname || f.username;
            conversationTitle = senderName;
          }
        }

        const activeConvId = !activeTarget
          ? null
          : activeTarget.kind === 'friend' && meId
          ? privateConvId(meId, activeTarget.id)
          : activeTarget.kind === 'channel'
          ? channelConvId(activeTarget.channelId)
          : null;

        // @-mention check: server stores mentions as numeric user IDs.
        const isMention = !!meId && (msg.mentions ?? []).map(String).includes(String(meId));

        // Bump unread counter unless this conv is currently focused.
        if (activeConvId !== msg.conversationId) {
          useChatStore.getState().bumpUnread(msg.conversationId, isMention);
        }

        if (notifyAllowed) {
          void maybeNotify(msg, { senderName, conversationTitle, activeConvId, isMention });
        }
        return;
      }
      if (ev.type === 'chat.recall') {
        const msg = ev.payload as ChatMessage;
        replaceMessage(msg);
        return;
      }
      // chat.edit fires when the author rewrites a text message within
      // the 5-min edit window. Same shape as chat.recv — the store's
      // replaceMessage swaps the existing row in place. We don't
      // surface notifications for edits (would be too noisy).
      if (ev.type === 'chat.edit') {
        const msg = ev.payload as ChatMessage;
        replaceMessage(msg);
        return;
      }
      // chat.delete fires when the author hard-deletes a message
      // (server PATCH /messages/:id). Server's copy is gone for good.
      // Phase 2 (local archive) will gate this event on the message's
      // createdAt being within 30 days — for now we trust everything
      // since we don't have a persistent archive yet.
      if (ev.type === 'chat.delete') {
        const p = ev.payload as { messageId: string; conversationId: string };
        useChatStore.getState().removeMessage(p.conversationId, p.messageId);
        return;
      }
      if (ev.type === 'chat.reaction') {
        const p = ev.payload as { conversationId: string; messageId: string; reactions: ReactionCountType[] };
        applyReactionUpdate(p.conversationId, p.messageId, p.reactions ?? []);
        return;
      }
      if (ev.type === 'chat.pin') {
        const p = ev.payload as { conversationId: string; messageId: string; pinnedBy: string };
        // We don't have the full Pin from the event — fetch the list to get
        // the snapshot. Cheap enough since pins are small.
        listPinsAndStore(p.conversationId);
        return;
      }
      if (ev.type === 'chat.unpin') {
        const p = ev.payload as { conversationId: string; messageId: string };
        removePin(p.conversationId, p.messageId);
        return;
      }
      if (ev.type === 'chat.read') {
        const p = ev.payload as { conversationId: string; userId: string; seq: number };
        setPeerRead(p.conversationId, p.userId, p.seq);
        return;
      }
      if (ev.type === 'friend.request') {
        // Bump the sidebar badge + show a toast.
        window.dispatchEvent(new Event('dfchat.friend-request'));
        toast('收到新的好友请求', 'info');
        return;
      }
      if (ev.type === 'friend.accepted') {
        // Refresh friends list so the new entry appears immediately on both sides.
        listFriends().then((fs) => setFriends(fs)).catch(() => {});
        toast('对方接受了好友请求', 'success');
        return;
      }
      if (ev.type.startsWith('call.')) {
        handleIncomingCallEvent(ev.type, ev.payload);
        return;
      }
      if (ev.type === 'live.host.golive') {
        // A host the user follows just went live. Pop a desktop notification
        // and offer to jump to /live.
        const p = ev.payload as { roomId: string; title: string; coverUrl?: string };
        window.electronAPI?.showNotification?.({
          title: '关注的主播开播了',
          body: p.title || '点开进入直播间',
          conversationId: '',
        });
        toast(`${p.title || '关注的主播'} 开播了`, 'info');
        return;
      }
      if (ev.type === 'live.host.offline') {
        const p = ev.payload as { title: string };
        toast(`${p.title || '关注的主播'} 已下播`, 'info');
        return;
      }
      if (ev.type === 'live.host.scheduled') {
        // A host we follow has scheduled a stream within the next ~10 min.
        const p = ev.payload as { title: string; coverUrl?: string };
        window.electronAPI?.showNotification?.({
          title: '关注的主播即将开播',
          body: p.title || '点开进入直播间',
          conversationId: '',
        });
        toast(`即将开播：${p.title || '关注的主播'}`, 'info');
        return;
      }
    });

    async function listPinsAndStore(convId: string) {
      try {
        const pins = await listPins(convId);
        useChatStore.getState().setPins(convId, pins as PinType[]);
      } catch { /* ignore */ }
    }

    const offOpen = wsClient.onOpen(() => {
      syncAllConversations();
    });

    const unsubSeq = useSeqStore.subscribe((state) => {
      const muted = useChatStore.getState().mutedConvs;
      let total = 0;
      for (const id of new Set([...Object.keys(state.last), ...Object.keys(state.read)])) {
        if (muted.has(id)) continue;
        total += Math.max(0, (state.last[id] ?? 0) - (state.read[id] ?? 0));
      }
      void setBadge(total);
    });

    const offActivate = window.electronAPI?.onActivateConversation?.((convId) => {
      const target = convIdToTarget(convId);
      if (target) useChatStore.getState().setActiveTarget(target);
    });

    // Refresh friend presence every 30s.
    const presenceTimer = window.setInterval(() => {
      listFriends().then((fs) => setFriends(fs)).catch(() => {});
    }, 30000);

    return () => {
      cancelled = true;
      offMsg();
      offOpen();
      unsubSeq();
      offActivate?.();
      clearInterval(presenceTimer);
      // DO NOT close the WebSocket here. Navigating Home → Live → Home
      // would otherwise tear down the WS during the page switch, and
      // any danmaku send from Live would hit "网络中断中" because the
      // socket was unilaterally closed by Home's cleanup. The WS is
      // app-lifetime now — only handleLogout closes it.
      void setBadge(0);
      useCallStore.getState().end();
    };
  }, [accessToken, navigate, setFriends, setGroups, setSession, appendMessage, replaceMessage, hydrateFromCache]);

  function convIdToTarget(convId: string): ChatTarget | null {
    if (convId.startsWith('c_')) {
      const channelId = convId.slice(2);
      const { channelsByGroup } = useChatStore.getState();
      for (const [gid, chs] of Object.entries(channelsByGroup)) {
        if (chs.some((c) => c.id === channelId)) {
          return { kind: 'channel', groupId: gid, channelId };
        }
      }
      return null;
    }
    if (convId.startsWith('p_')) {
      const [, a, b] = convId.split('_');
      const meId = useUserStore.getState().user?.id;
      if (!meId) return null;
      const other = a === meId ? b : a;
      return { kind: 'friend', id: other };
    }
    return null;
  }

  async function handleLogout() {
    // Best-effort server-side revoke first (refresh token in localStorage).
    try {
      const { logoutServer } = await import('@/api/client');
      await logoutServer();
    } catch { /* ignore */ }
    wsClient.close();
    useSeqStore.getState().clearAll();
    clear();
    navigate('/login', { replace: true });
  }

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-bg-1">
        <div className="text-ink-3 flex items-center gap-2 anim-fade">
          <span className="w-5 h-5 rounded-full border-2 border-brand-500 border-r-transparent animate-spin" />
          正在加载…
        </div>
      </div>
    );
  }
  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-bg-1">
        <div className="text-accent-red">{error}</div>
      </div>
    );
  }
  if (!user) return null;

  return (
    <div className="h-screen flex flex-col bg-bg-1 text-ink-1">
      <TitleBar title="东风快信" />
      {!user.emailVerified && <EmailVerifyBanner />}
      <div className="flex flex-1 min-h-0">
        <FriendSidebar
          onLogout={handleLogout}
          onOpenAdmin={() => navigate('/admin')}
          onOpenSearch={() => setSearchOpen(true)}
          onOpenSettings={() => navigate('/settings')}
          onOpenLive={() => navigate('/live')}
        />
        <ChatView />
        <CallOverlay />
      </div>
      <SearchModal open={searchOpen} onClose={() => setSearchOpen(false)} />
    </div>
  );
}

function EmailVerifyBanner() {
  const [dismissed, setDismissed] = useState(false);
  const [sending, setSending] = useState(false);
  const [sentAt, setSentAt] = useState<number | null>(null);

  if (dismissed) return null;

  async function resend() {
    setSending(true);
    try {
      const { sendVerificationEmail } = await import('@/api/client');
      const res = await sendVerificationEmail();
      if (res.alreadyVerified) {
        toast('邮箱已验证 — 刷新页面查看', 'info');
        setDismissed(true);
        return;
      }
      setSentAt(Date.now());
      if (res.devLink) {
        toast('开发模式：验证链接已 log，控制台可见', 'info');
      } else {
        toast('验证邮件已发送，去邮箱查收', 'success');
      }
    } catch (e: any) {
      toast(e.message ?? '发送失败', 'error');
    } finally {
      setSending(false);
    }
  }

  const cooled = !sentAt || Date.now() - sentAt > 60000;

  return (
    <div className="bg-accent-amber/15 border-b border-accent-amber/40 px-4 py-2 text-sm flex items-center gap-3">
      <span className="text-accent-amber">⚠️</span>
      <span className="text-ink-2 flex-1">
        你的邮箱还未验证。验证后才能在忘记密码时收到重置邮件。
      </span>
      <button
        onClick={resend}
        disabled={sending || !cooled}
        className="btn-secondary text-xs py-1"
      >
        {sending ? '发送中…' : (sentAt ? (cooled ? '重新发送' : '已发送') : '发送验证邮件')}
      </button>
      <button
        onClick={() => setDismissed(true)}
        className="btn-icon w-6 h-6 text-ink-3"
        title="本次会话隐藏"
      >
        ×
      </button>
    </div>
  );
}
