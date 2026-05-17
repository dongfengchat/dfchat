import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Bell,
  BellOff,
  CornerDownLeft,
  Loader2,
  MoreHorizontal,
  MoreVertical,
  Paperclip,
  Phone,
  Pin as PinIcon,
  PinOff,
  Reply,
  RotateCcw,
  Send,
  ShieldOff,
  Smile,
  SmilePlus,
  Trash2,
  Users,
  Video,
  X,
} from 'lucide-react';
import {
  addReaction as apiAddReaction,
  blockUser,
  channelConvId,
  listChannels,
  listFriends,
  listGroupMembers,
  listGroups,
  listMessages,
  listMessagesAround,
  listPins,
  markRead as apiMarkRead,
  pinMessage,
  privateConvId,
  deleteMessage,
  editMessage,
  recallMessage,
  removeReaction as apiRemoveReaction,
  sendChannelMessage,
  sendChannelRich,
  sendPrivateMessage,
  sendPrivateRich,
  setConversationPreferences,
  unpinMessage,
  uploadBlob,
} from '@/api/client';
import { useChatStore } from '@/store/chatStore';
import { wsClient } from '@/ws/client';
import { useUserStore } from '@/store/userStore';
import { useSeqStore } from '@/sync/seqStore';
import { useCallStore } from '@/call/store';
import { imageDimensions } from '@/utils/image';
import { renderMarkdown } from '@/utils/markdown';
import Avatar from './ui/Avatar';
import { ChatSkeleton } from './ui/Skeleton';
import { toast } from './ui/Toast';
import EmojiPicker from './ui/EmojiPicker';
import MembersPanel from './MembersPanel';
import EditGroupDialog from './EditGroupDialog';
import GroupFilesPanel from './GroupFilesPanel';
import { Megaphone, Pencil, Users2, X as IconX } from 'lucide-react';
import type { ChatMessage, GroupMember } from '@/types';

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function dayLabel(d: Date): string {
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (isSameDay(d, today)) return '今天';
  if (isSameDay(d, yesterday)) return '昨天';
  const sameYear = d.getFullYear() === today.getFullYear();
  return sameYear
    ? `${d.getMonth() + 1} 月 ${d.getDate()} 日`
    : `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function timeLabel(d: Date): string {
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
}

function previewOf(msg: ChatMessage | undefined): string {
  if (!msg) return '原消息已不存在';
  if (msg.isRecalled) return '[消息已撤回]';
  if (msg.type === 'text' && typeof msg.content?.text === 'string') {
    const t = msg.content.text as string;
    return t.length > 60 ? `${t.slice(0, 60)}…` : t;
  }
  if (msg.type === 'image') return '[图片]';
  if (msg.type === 'file') return `[文件] ${msg.content?.name as string || ''}`;
  return '[消息]';
}

function MessageBody({
  msg,
  nameLookup,
}: {
  msg: ChatMessage;
  nameLookup: Map<string, string>;
}) {
  if (msg.isRecalled) {
    return <span className="italic text-ink-4">[消息已撤回]</span>;
  }
  const knownHandles = useMemo(() => new Set(Array.from(nameLookup.values()).map((s) => s.toLowerCase())), [nameLookup]);
  const isKnown = (h: string) => knownHandles.has(h.toLowerCase());

  if (msg.type === 'text') {
    const text = typeof msg.content?.text === 'string' ? msg.content.text : '';
    return <div className="text-sm leading-relaxed">{renderMarkdown(text, isKnown)}</div>;
  }
  if (msg.type === 'image') {
    const url = msg.content?.url as string | undefined;
    const w = (msg.content?.width as number) || 0;
    const h = (msg.content?.height as number) || 0;
    if (!url) return <>[图片缺失]</>;
    const aspect = w && h ? `${w} / ${h}` : undefined;
    return (
      <a href={url} target="_blank" rel="noreferrer" className="block">
        <img
          src={url}
          className="rounded-lg max-w-full max-h-80 object-contain bg-bg-3"
          style={aspect ? { aspectRatio: aspect } : undefined}
          loading="lazy"
          alt={(msg.content?.name as string) || 'image'}
        />
      </a>
    );
  }
  if (msg.type === 'file') {
    const url = msg.content?.url as string | undefined;
    const name = (msg.content?.name as string) || 'file';
    const size = (msg.content?.size as number) || 0;
    return (
      <a
        href={url}
        target="_blank"
        rel="noreferrer"
        className="flex items-center gap-3 bg-bg-4 hover:bg-bg-5 transition-colors rounded-lg px-3 py-2 max-w-sm"
      >
        <span className="w-9 h-9 rounded-lg bg-brand-500/20 flex items-center justify-center text-brand-300">📄</span>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium text-ink-1">{name}</span>
          <span className="block text-[11px] text-ink-3">{formatBytes(size)}</span>
        </span>
      </a>
    );
  }
  return <>[未知消息类型 {msg.type}]</>;
}

function withinRecallWindow(createdAt: string): boolean {
  return Date.now() - new Date(createdAt).getTime() < 2 * 60 * 1000;
}

// withinEditWindow mirrors the server-side EditWindowSeconds=300. The
// menu hides the "编辑" item once the window passes so users don't
// click into a sure-to-fail PATCH.
function withinEditWindow(createdAt: string): boolean {
  return Date.now() - new Date(createdAt).getTime() < 5 * 60 * 1000;
}

// Returns the id of the last own-message in the whole conversation. Used so
// only one "已读" tail is rendered, not on every own bubble.
function lastOwnMessageId(messages: ChatMessage[], myId: string | undefined): string | null {
  if (!myId) return null;
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].senderId === myId && !messages[i].isRecalled) return messages[i].id;
  }
  return null;
}

function isLastMineMessage(groupMsgs: ChatMessage[], myId: string | undefined, allMsgs: ChatMessage[]): string | null {
  const lastId = lastOwnMessageId(allMsgs, myId);
  if (!lastId) return null;
  if (groupMsgs.some((m) => m.id === lastId)) return lastId;
  return null;
}

interface RenderItem {
  type: 'date' | 'group';
  key: string;
  date?: Date;
  msgs?: ChatMessage[];
}

function buildRenderList(messages: ChatMessage[]): RenderItem[] {
  const out: RenderItem[] = [];
  let lastDate: Date | null = null;
  let currentGroup: ChatMessage[] = [];
  let groupKey = 0;

  function flush() {
    if (currentGroup.length > 0) {
      out.push({ type: 'group', key: `g-${groupKey++}`, msgs: currentGroup });
      currentGroup = [];
    }
  }

  for (const m of messages) {
    const d = new Date(m.createdAt);
    if (!lastDate || !isSameDay(lastDate, d)) {
      flush();
      out.push({ type: 'date', key: `d-${d.toDateString()}`, date: d });
      lastDate = d;
    }
    if (currentGroup.length === 0) {
      currentGroup.push(m);
    } else {
      const last = currentGroup[currentGroup.length - 1];
      const sameSender = last.senderId === m.senderId;
      const close = new Date(m.createdAt).getTime() - new Date(last.createdAt).getTime() < 5 * 60 * 1000;
      // Don't merge a reply with surrounding messages.
      if (sameSender && close && !m.replyTo) currentGroup.push(m);
      else {
        flush();
        currentGroup.push(m);
      }
    }
  }
  flush();
  return out;
}

export default function ChatView() {
  const me = useUserStore((s) => s.user);
  const friends = useChatStore((s) => s.friends);
  const setFriends = useChatStore((s) => s.setFriends);
  const setGroups = useChatStore((s) => s.setGroups);
  const setActiveTarget = useChatStore((s) => s.setActiveTarget);
  const groups = useChatStore((s) => s.groups);
  const channelsByGroup = useChatStore((s) => s.channelsByGroup);
  const activeTarget = useChatStore((s) => s.activeTarget);
  const setMessages = useChatStore((s) => s.setMessages);
  const appendMessage = useChatStore((s) => s.appendMessage);
  const replaceMessage = useChatStore((s) => s.replaceMessage);
  const setChannels = useChatStore((s) => s.setChannels);
  const messagesByConv = useChatStore((s) => s.messagesByConv);
  const pinsByConv = useChatStore((s) => s.pinsByConv);
  const setPins = useChatStore((s) => s.setPins);
  const peerLastReadSeq = useChatStore((s) => s.peerLastReadSeq);
  const applyReactionUpdate = useChatStore((s) => s.applyReactionUpdate);
  const mergeMessages = useChatStore((s) => s.mergeMessages);
  const pendingJump = useChatStore((s) => s.pendingJump);
  const setPendingJump = useChatStore((s) => s.setPendingJump);
  const mutedConvs = useChatStore((s) => s.mutedConvs);
  const setMutedConv = useChatStore((s) => s.setMuted);

  const ctx = useMemo(() => {
    if (!me || !activeTarget) return null;
    if (activeTarget.kind === 'friend') {
      const f = friends.find((x) => x.id === activeTarget.id);
      if (!f) return null;
      return {
        kind: 'friend' as const,
        title: f.nickname || f.username,
        subtitle: f.isOnline ? <span className="text-accent-green">在线</span> : `@${f.username}`,
        avatarName: f.nickname || f.username,
        online: !!f.isOnline,
        convId: privateConvId(me.id, f.id),
        targetId: f.id,
        canCall: true,
      };
    }
    const g = groups.find((x) => x.id === activeTarget.groupId);
    const ch = (channelsByGroup[activeTarget.groupId] || []).find((c) => c.id === activeTarget.channelId);
    if (!g || !ch) return null;
    return {
      kind: 'channel' as const,
      title: `#${ch.name}`,
      subtitle: `${g.name} · ${g.memberCount} 人`,
      avatarName: g.name,
      online: null as boolean | null,
      inviteCode: g.inviteCode,
      convId: channelConvId(ch.id),
      groupId: g.id,
      channelId: ch.id,
      canCall: false,
    };
  }, [me, activeTarget, friends, groups, channelsByGroup]);

  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [members, setMembers] = useState<GroupMember[] | null>(null);
  const [uploading, setUploading] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [loadingHistory, setLoadingHistory] = useState(false);
  const [replyingTo, setReplyingTo] = useState<ChatMessage | null>(null);
  // Inline-edit state. `editingId` is the id of the message whose
  // bubble has been swapped out for a small textarea; `editingDraft`
  // is the local working text. Both clear on save / cancel / Esc.
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingDraft, setEditingDraft] = useState('');
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [reactingFor, setReactingFor] = useState<string | null>(null);
  const [pinnedExpanded, setPinnedExpanded] = useState(false);
  const [membersOpen, setMembersOpen] = useState(false);
  const [editGroupOpen, setEditGroupOpen] = useState(false);
  const [filesPanelOpen, setFilesPanelOpen] = useState(false);
  const [announcementCollapsed, setAnnouncementCollapsed] = useState(false);
  // typingPeers[senderId] = expireTs (ms). Auto-clears 5 s after the last
  // typing.start (or immediately on typing.stop). Only counts events for
  // the current conversation.
  const [typingPeers, setTypingPeers] = useState<Record<string, number>>({});
  const lastTypingSentRef = useRef(0);
  const [headerMenuOpen, setHeaderMenuOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Listen for typing events. Filter to the conversation we're currently
  // showing; entries auto-expire after 5 s (continuous typing re-extends).
  useEffect(() => {
    if (!ctx) return;
    setTypingPeers({});
    const off = wsClient.on((ev) => {
      if (ev.type !== 'typing.start' && ev.type !== 'typing.stop') return;
      const p = ev.payload as { conversationId: string; senderId: string };
      if (p.conversationId !== ctx.convId) return;
      setTypingPeers((cur) => {
        const next = { ...cur };
        if (ev.type === 'typing.stop') {
          delete next[p.senderId];
        } else {
          next[p.senderId] = Date.now() + 5000;
        }
        return next;
      });
    });
    // Periodic sweep — drop entries whose ttl passed.
    const t = setInterval(() => {
      setTypingPeers((cur) => {
        const now = Date.now();
        let changed = false;
        const next: Record<string, number> = {};
        for (const [k, v] of Object.entries(cur)) {
          if (v > now) next[k] = v;
          else changed = true;
        }
        return changed ? next : cur;
      });
    }, 1500);
    return () => { off(); clearInterval(t); };
  }, [ctx?.convId]);

  // Throttled typing-start sender. Friend conversations only for now — we
  // can extend to channel/group once the client tracks members eagerly.
  function sendTypingPing() {
    if (!ctx || ctx.kind !== 'friend') return;
    const now = Date.now();
    if (now - lastTypingSentRef.current < 3000) return; // ≤ 1 ping / 3 s
    lastTypingSentRef.current = now;
    wsClient.send('typing.start', {
      conversationId: ctx.convId,
      recipientIds: [ctx.targetId],
    });
  }

  useEffect(() => {
    if (!ctx) return;
    // Entering a conv clears its sidebar unread badge.
    useChatStore.getState().clearUnread(ctx.convId);
    let cancelled = false;
    setLoadingHistory(true);
    setReplyingTo(null);

    // Instant hydrate from the encrypted local archive (if available)
    // so the user sees their previous messages immediately — including
    // messages older than the server's 30-day retention window. The
    // server fetch below then layers the latest server-state on top,
    // overwriting any rows that still exist server-side and adding
    // anything new that arrived while we were offline.
    const archive = (typeof window !== 'undefined' && window.dfchatArchive) || undefined;
    if (archive) {
      archive
        .queryByConv(ctx.convId, 200)
        .then((rows) => {
          if (cancelled || rows.length === 0) return;
          // queryByConv returns newest-first; setMessages expects
          // newest-first too (it reverses internally). Just hand it over.
          const mapped: ChatMessage[] = rows.map((r) => ({
            id: r.id,
            conversationId: r.conversationId,
            senderId: r.senderId,
            type: r.type,
            content: r.content as ChatMessage['content'],
            seq: r.seq,
            mentions: r.mentions?.map((s) => Number(s)),
            replyTo: r.replyTo != null ? Number(r.replyTo) : undefined,
            isRecalled: r.isRecalled,
            editedAt: r.editedAt,
            editCount: r.editCount,
            createdAt: r.createdAt,
          }));
          // Use mergeMessages so the server pull below can layer over
          // without losing locally-archived rows past the server's
          // 30-day window.
          useChatStore.getState().mergeMessages(ctx.convId, mapped);
        })
        .catch(() => { /* archive missing → no-op, server pull continues */ });
    }

    listMessages(ctx.convId)
      .then((msgs) => {
        if (cancelled) return;
        if (archive) {
          // We already showed the archive; merge in server's latest so
          // edits / recalls / deletes within the 30-day window land.
          useChatStore.getState().mergeMessages(ctx.convId, msgs);
        } else {
          setMessages(ctx.convId, msgs);
        }
      })
      .catch(() => {})
      .finally(() => !cancelled && setLoadingHistory(false));
    // Load pins for this conversation.
    listPins(ctx.convId)
      .then((pins) => !cancelled && setPins(ctx.convId, pins))
      .catch(() => {});
    if (ctx.kind === 'channel') {
      listGroupMembers(ctx.groupId).then((m) => !cancelled && setMembers(m)).catch(() => {});
      if (!channelsByGroup[ctx.groupId]) {
        listChannels(ctx.groupId).then((chs) => !cancelled && setChannels(ctx.groupId, chs)).catch(() => {});
      }
    } else {
      setMembers(null);
    }
    return () => {
      cancelled = true;
    };
  }, [ctx?.convId]);

  const messages = ctx ? messagesByConv[ctx.convId] ?? [] : [];
  const renderList = useMemo(() => buildRenderList(messages), [messages]);

  // Map id → message for replyTo lookup.
  const msgById = useMemo(() => {
    const map = new Map<string, ChatMessage>();
    messages.forEach((m) => map.set(m.id, m));
    return map;
  }, [messages]);

  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    if (ctx && messages.length > 0) {
      const headSeq = messages[messages.length - 1].seq;
      useSeqStore.getState().markRead(ctx.convId, headSeq);
      // Inform the server so peers can see "已读" on their messages.
      // Fire-and-forget; small throttle to avoid hammering.
      void apiMarkRead(ctx.convId, headSeq).catch(() => {});
    }
  }, [messages.length, ctx?.convId]);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 6 * 24 + 16)}px`;
  }, [text]);

  // Focus textarea when entering reply mode.
  useEffect(() => {
    if (replyingTo) textareaRef.current?.focus();
  }, [replyingTo?.id]);

  // Handle a pending jump-to-message (from search). Fetch a window around
  // the seq if we don't already have it, then scroll + flash the bubble.
  useEffect(() => {
    if (!ctx || !pendingJump) return;
    if (pendingJump.convId !== ctx.convId) return;
    let cancelled = false;
    (async () => {
      const have = messages.some((m) => m.id === pendingJump.messageId);
      if (!have) {
        try {
          const around = await listMessagesAround(ctx.convId, pendingJump.seq, 40);
          if (!cancelled) mergeMessages(ctx.convId, around);
        } catch { /* ignore */ }
      }
      // Wait a tick for the DOM to render the bubble, then scroll + flash.
      requestAnimationFrame(() => {
        const el = document.getElementById(`msg-${pendingJump.messageId}`);
        if (!el || !scrollRef.current) return;
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        el.classList.add('jump-flash');
        setTimeout(() => el.classList.remove('jump-flash'), 1600);
        setPendingJump(null);
      });
    })();
    return () => { cancelled = true; };
  }, [pendingJump?.messageId, pendingJump?.convId, ctx?.convId, messages.length, mergeMessages, setPendingJump]);

  const nameLookup = useMemo(() => {
    const map = new Map<string, string>();
    if (me) map.set(me.id, me.nickname || me.username);
    friends.forEach((f) => map.set(f.id, f.nickname || f.username));
    members?.forEach((m) => map.set(m.userId, m.nickname || m.username));
    return map;
  }, [me, friends, members]);

  const handleToId = useMemo(() => {
    const map = new Map<string, string>();
    for (const [id, name] of nameLookup) map.set(name.toLowerCase(), id);
    return map;
  }, [nameLookup]);

  function extractMentions(input: string): string[] {
    const re = /@([A-Za-z0-9_]+)/g;
    const out = new Set<string>();
    let m: RegExpExecArray | null;
    while ((m = re.exec(input))) {
      const id = handleToId.get(m[1].toLowerCase());
      if (id) out.add(id);
    }
    return Array.from(out);
  }

  async function handleSendText() {
    if (!ctx || !text.trim()) return;
    setError(null);
    setSending(true);
    try {
      const mentions = extractMentions(text);
      const replyTo = replyingTo?.id;
      const m =
        ctx.kind === 'friend'
          ? await sendPrivateMessage(ctx.targetId, text.trim(), { mentions, replyTo })
          : await sendChannelMessage(ctx.channelId, text.trim(), { mentions, replyTo });
      appendMessage(m);
      setText('');
      setReplyingTo(null);
    } catch (err: any) {
      setError(err.message ?? '发送失败');
    } finally {
      setSending(false);
    }
  }

  async function handleUpload(file: File) {
    if (!ctx) return;
    setError(null);
    const isImage = file.type.startsWith('image/');
    const kind = isImage ? 'image' : 'file';
    setUploading(`上传 ${file.name}…`);
    try {
      let dims = { width: 0, height: 0 };
      if (isImage) dims = await imageDimensions(file);
      const uploaded = await uploadBlob(file, file.name, kind);
      const content: Record<string, unknown> = {
        fileId: uploaded.id,
        url: uploaded.url,
        name: uploaded.name,
        size: uploaded.size,
        mime: uploaded.mime ?? file.type,
      };
      if (isImage) {
        content.width = dims.width;
        content.height = dims.height;
      }
      const replyTo = replyingTo?.id;
      const m =
        ctx.kind === 'friend'
          ? await sendPrivateRich(ctx.targetId, kind, content, replyTo)
          : await sendChannelRich(ctx.channelId, kind, content, replyTo);
      appendMessage(m);
      setReplyingTo(null);
    } catch (err: any) {
      setError(err.message ?? '上传失败');
    } finally {
      setUploading(null);
    }
  }

  async function handleRecall(msg: ChatMessage) {
    setMenuFor(null);
    try {
      const updated = await recallMessage(msg.id);
      replaceMessage(updated);
      toast('已撤回', 'success');
    } catch (err: any) {
      toast(err.message ?? '撤回失败', 'error');
    }
  }

  // handleDelete permanently removes the message from the server. Note
  // this is different from recall — recall keeps a redacted "撤回"
  // placeholder; delete drops the row entirely (so reply quotes need
  // the _replyToSnapshot we embed at send time). Local archive (Phase 2)
  // is unaffected by design — that's the whole point of the 30-day
  // server authority horizon.
  async function handleDelete(msg: ChatMessage) {
    setMenuFor(null);
    if (!confirm('删除这条消息？\n\n此操作会从服务端永久移除该消息（30 天内有效）。其他设备上的本地副本可能仍保留——这是"服务端 30 天保留"的设计。')) return;
    try {
      await deleteMessage(msg.id);
      useChatStore.getState().removeMessage(msg.conversationId, msg.id);
      toast('已删除', 'success');
    } catch (err: any) {
      toast(err.message ?? '删除失败', 'error');
    }
  }

  // handleStartEdit swaps the bubble out for an inline textarea
  // populated with the current text. Closes any open right-click menu.
  function handleStartEdit(msg: ChatMessage) {
    setMenuFor(null);
    const current = typeof msg.content?.text === 'string' ? msg.content.text : '';
    setEditingId(msg.id);
    setEditingDraft(current);
  }

  function handleCancelEdit() {
    setEditingId(null);
    setEditingDraft('');
  }

  async function handleSaveEdit(msg: ChatMessage) {
    const trimmed = editingDraft.trim();
    if (!trimmed) {
      toast('内容不能为空', 'warn');
      return;
    }
    // No-op if unchanged — saves a server round-trip and avoids
    // bumping editCount on accidental Enter.
    const original = typeof msg.content?.text === 'string' ? msg.content.text : '';
    if (trimmed === original.trim()) {
      handleCancelEdit();
      return;
    }
    try {
      const updated = await editMessage(msg.id, editingDraft);
      replaceMessage(updated);
      handleCancelEdit();
    } catch (err: any) {
      toast(err.message ?? '编辑失败', 'error');
    }
  }

  async function handleToggleReaction(msg: ChatMessage, emoji: string) {
    if (!me) return;
    const myId = Number(me.id);
    const mine = (msg.reactions ?? []).find((r) => r.emoji === emoji)?.userIds?.includes(myId);
    // Optimistic apply: nudge the store immediately so the chip flips fast.
    const current = msg.reactions ?? [];
    let next = current.slice();
    const idx = next.findIndex((r) => r.emoji === emoji);
    if (mine) {
      if (idx >= 0) {
        const stripped = (next[idx].userIds ?? []).filter((u) => u !== myId);
        if (stripped.length === 0) next.splice(idx, 1);
        else next[idx] = { ...next[idx], count: next[idx].count - 1, userIds: stripped };
      }
    } else {
      if (idx >= 0) next[idx] = { ...next[idx], count: next[idx].count + 1, userIds: [...(next[idx].userIds ?? []), myId] };
      else next.push({ emoji, count: 1, userIds: [myId] });
    }
    applyReactionUpdate(msg.conversationId, msg.id, next);
    try {
      if (mine) await apiRemoveReaction(msg.id, emoji);
      else await apiAddReaction(msg.id, emoji);
    } catch (err: any) {
      // Roll back via authoritative refresh on next event; keep optimistic for now
      toast(err.message ?? '表情更新失败', 'error');
    }
  }

  async function handlePin(msg: ChatMessage) {
    setMenuFor(null);
    try {
      await pinMessage(msg.id);
      toast('已置顶', 'success');
    } catch (err: any) {
      toast(err.message ?? '置顶失败', 'error');
    }
  }

  async function handleUnpin(messageId: string) {
    try {
      await unpinMessage(messageId);
      toast('已取消置顶', 'success');
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }

  function insertAtCursor(s: string) {
    const el = textareaRef.current;
    if (!el) {
      setText((t) => t + s);
      return;
    }
    const start = el.selectionStart ?? text.length;
    const end = el.selectionEnd ?? text.length;
    const next = text.slice(0, start) + s + text.slice(end);
    setText(next);
    queueMicrotask(() => {
      if (textareaRef.current) {
        const pos = start + s.length;
        textareaRef.current.focus();
        textareaRef.current.setSelectionRange(pos, pos);
      }
    });
  }

  function onPaste(e: React.ClipboardEvent<HTMLDivElement>) {
    if (!ctx) return;
    for (const item of Array.from(e.clipboardData.items)) {
      if (item.kind === 'file') {
        const file = item.getAsFile();
        if (file) {
          e.preventDefault();
          void handleUpload(file);
          return;
        }
      }
    }
  }

  function onDrop(e: React.DragEvent<HTMLDivElement>) {
    e.preventDefault();
    setDragOver(false);
    if (!ctx) return;
    const files = Array.from(e.dataTransfer.files);
    files.forEach((f) => void handleUpload(f));
  }

  function startCall(kind: 'audio' | 'video') {
    if (!ctx || ctx.kind !== 'friend') return;
    const friend = friends.find((f) => f.id === ctx.targetId);
    if (!friend) return;
    void useCallStore.getState().startCall(friend.id, friend.nickname || friend.username, kind);
  }

  if (!ctx) {
    return (
      <main className="flex-1 flex items-center justify-center text-ink-3 bg-bg-1">
        <div className="text-center max-w-sm anim-fade">
          <div className="w-16 h-16 rounded-full bg-bg-3 flex items-center justify-center mx-auto mb-4">
            <Users size={28} className="text-ink-3" />
          </div>
          <div className="text-lg font-medium text-ink-1">选择一个会话开始聊天</div>
          <div className="text-sm text-ink-3 mt-1">从左侧选好友或频道，或使用顶部搜索框查找。</div>
        </div>
      </main>
    );
  }

  return (
    <main
      className="flex-1 flex flex-col relative bg-bg-1 min-w-0"
      onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
      onDragLeave={() => setDragOver(false)}
      onDrop={onDrop}
      onPaste={onPaste}
      onClick={() => setMenuFor(null)}
    >
      {dragOver && (
        <div className="absolute inset-0 z-10 bg-brand-500/10 border-4 border-dashed border-brand-400 rounded-lg m-2 flex items-center justify-center pointer-events-none">
          <div className="text-brand-200 text-lg font-medium">松开发送到 {ctx.title}</div>
        </div>
      )}

      <header className="h-14 px-4 border-b border-bg-5/40 bg-bg-2/60 backdrop-blur flex items-center gap-3 shrink-0">
        <Avatar name={ctx.avatarName} size={32} online={ctx.kind === 'friend' ? ctx.online : null} />
        <div className="min-w-0">
          <div className="font-medium truncate text-ink-1">{ctx.title}</div>
          <div className="text-xs text-ink-3 truncate">
            {ctx.subtitle}
            {ctx.kind === 'channel' && (
              <span className="ml-2 text-ink-4">· 邀请码 <code className="bg-bg-3 px-1 py-0.5 rounded text-[10px]">{ctx.inviteCode}</code></span>
            )}
          </div>
        </div>
        <div className="ml-auto flex items-center gap-1">
          {(() => {
            const muted = mutedConvs.has(ctx.convId);
            return (
              <button
                onClick={async () => {
                  try {
                    await setConversationPreferences(ctx.convId, !muted);
                    setMutedConv(ctx.convId, !muted);
                    toast(muted ? '已取消免打扰' : '已开启免打扰', 'success');
                  } catch (err: any) {
                    toast(err.message ?? '操作失败', 'error');
                  }
                }}
                className="btn-icon"
                title={muted ? '取消免打扰' : '免打扰'}
              >
                {muted ? <BellOff size={18} className="text-ink-3" /> : <Bell size={18} />}
              </button>
            );
          })()}
          {ctx.canCall && (
            <>
              <button onClick={() => startCall('audio')} className="btn-icon text-accent-green hover:text-accent-green" title="语音通话">
                <Phone size={18} />
              </button>
              <button onClick={() => startCall('video')} className="btn-icon text-brand-300 hover:text-brand-200" title="视频通话">
                <Video size={18} />
              </button>
            </>
          )}
          {ctx.kind === 'channel' && (() => {
            const g = groups.find((x) => x.id === ctx.groupId);
            const isOwnerOrAdmin = !!g && (g.ownerId === me?.id);
            return (
              <>
                {g && isOwnerOrAdmin && (
                  <button onClick={() => setEditGroupOpen(true)} className="btn-icon" title="编辑群信息">
                    <Pencil size={18} />
                  </button>
                )}
                <button onClick={() => setFilesPanelOpen(true)} className="btn-icon" title="群文件">
                  <Paperclip size={18} />
                </button>
                <button onClick={() => setMembersOpen(true)} className="btn-icon" title="群成员">
                  <Users2 size={18} />
                </button>
              </>
            );
          })()}
          {ctx.kind === 'friend' && (
            <div className="relative">
              <button
                onClick={() => setHeaderMenuOpen((v) => !v)}
                className="btn-icon"
                title="更多"
              >
                <MoreVertical size={18} />
              </button>
              {headerMenuOpen && (
                <div className="absolute right-0 top-full mt-1 z-20 bg-bg-3 border border-bg-5/40 rounded-lg shadow-pop min-w-[140px] overflow-hidden anim-scale">
                  <button
                    onClick={async () => {
                      setHeaderMenuOpen(false);
                      if (ctx.kind !== 'friend') return;
                      const f = friends.find((x) => x.id === ctx.targetId);
                      if (!f) return;
                      if (!confirm(`屏蔽 ${f.nickname || f.username}？\n屏蔽后你们将无法互相收发消息。`)) return;
                      try {
                        await blockUser(f.id);
                        setFriends(await listFriends());
                        setActiveTarget(null);
                        toast('已屏蔽', 'success');
                      } catch (err: any) {
                        toast(err.message ?? '操作失败', 'error');
                      }
                    }}
                    className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left text-accent-red hover:bg-bg-4"
                  >
                    <ShieldOff size={14} /> 屏蔽并删除好友
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </header>

      {ctx.kind === 'channel' && (() => {
        const g = groups.find((x) => x.id === ctx.groupId);
        if (!g) return null;
        const myRole = g.ownerId === me?.id ? 2 : 0;
        return (
          <>
            <MembersPanel
              open={membersOpen}
              onClose={() => setMembersOpen(false)}
              groupId={g.id}
              groupName={g.name}
              ownerId={g.ownerId}
            />
            <EditGroupDialog
              open={editGroupOpen}
              group={g}
              onClose={() => setEditGroupOpen(false)}
              onSaved={async (updated) => {
                // Don't auto-close on every save — invite-rotate /
                // privacy-toggle / iconUrl-upload all call onSaved and
                // the user usually wants to keep the dialog open.
                // We only close from the dedicated "保存" + "取消"
                // buttons in the dialog (which both call onClose).
                try {
                  const fresh = await listGroups();
                  setGroups(fresh);
                  void updated;
                } catch { /* keep cached */ }
              }}
              onDissolved={async () => {
                // Owner just dissolved — pop the user back to the
                // group list, refresh local store, drop the active
                // selection so we don't try to render a dead conv.
                try {
                  const fresh = await listGroups();
                  setGroups(fresh);
                } catch { /* keep cached */ }
                setActiveTarget(null);
              }}
              myRole={myRole}
            />
            <GroupFilesPanel
              open={filesPanelOpen}
              onClose={() => setFilesPanelOpen(false)}
              conversationId={ctx.convId}
              title={g.name}
            />
            {/* Group announcement banner. Collapsible so it doesn't eat space
                permanently once read. */}
            {g.announcement && !announcementCollapsed && (
              <div className="px-4 py-2.5 bg-brand-500/10 border-b border-brand-500/20 text-sm text-ink-2 flex items-start gap-2">
                <Megaphone size={14} className="text-brand-300 mt-0.5 shrink-0" />
                <div className="flex-1 min-w-0 whitespace-pre-wrap">
                  <span className="text-brand-300 font-medium">群公告 · </span>
                  {g.announcement}
                </div>
                <button
                  onClick={() => setAnnouncementCollapsed(true)}
                  className="btn-icon w-6 h-6 shrink-0"
                  title="收起"
                >
                  <IconX size={12} />
                </button>
              </div>
            )}
          </>
        );
      })()}

      {/* Pinned bar */}
      {(() => {
        const pins = pinsByConv[ctx.convId] ?? [];
        if (pins.length === 0) return null;
        return (
          <div className="px-4 py-2 bg-amber-500/5 border-b border-amber-500/20 text-xs">
            <button
              onClick={() => setPinnedExpanded((v) => !v)}
              className="flex items-center gap-1.5 text-amber-300 hover:text-amber-200"
            >
              <PinIcon size={12} /> {pins.length} 条置顶消息
              <span className="text-ink-4">{pinnedExpanded ? '收起' : '展开'}</span>
            </button>
            {pinnedExpanded && (
              <div className="mt-2 space-y-1 max-h-32 overflow-y-auto pr-1">
                {pins.map((p) => (
                  <div key={p.messageId} className="flex items-start gap-2 bg-bg-3 rounded-lg px-2 py-1.5">
                    <PinIcon size={11} className="text-amber-300 mt-1 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <div className="text-[11px] text-ink-3">
                        {nameLookup.get(p.message?.senderId ?? '') ?? `用户 ${p.message?.senderId ?? ''}`}
                      </div>
                      <div className="text-ink-2 truncate text-xs">{previewOf(p.message)}</div>
                    </div>
                    <button
                      onClick={() => handleUnpin(p.messageId)}
                      className="btn-icon w-6 h-6"
                      title="取消置顶"
                    >
                      <PinOff size={12} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })()}

      <div ref={scrollRef} className="flex-1 overflow-y-auto py-3 px-4">
        {loadingHistory && messages.length === 0 ? (
          <ChatSkeleton />
        ) : messages.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center text-ink-4 text-sm">
            <div className="w-12 h-12 rounded-full bg-bg-3 flex items-center justify-center mb-3">👋</div>
            还没有消息，发条招呼吧
          </div>
        ) : (
          <div className="max-w-3xl mx-auto space-y-1">
            {renderList.map((item) => {
              if (item.type === 'date' && item.date) {
                return (
                  <div key={item.key} className="flex items-center gap-3 my-4">
                    <div className="flex-1 border-t border-bg-5/40" />
                    <div className="text-[11px] uppercase tracking-wider text-ink-4">{dayLabel(item.date)}</div>
                    <div className="flex-1 border-t border-bg-5/40" />
                  </div>
                );
              }
              if (item.type !== 'group' || !item.msgs?.length) return null;
              const first = item.msgs[0];
              const mine = first.senderId === me?.id;
              const senderName =
                ctx.kind === 'channel'
                  ? members?.find((x) => x.userId === first.senderId)?.nickname ?? nameLookup.get(first.senderId) ?? `用户 ${first.senderId}`
                  : nameLookup.get(first.senderId) ?? '';
              return (
                <MessageGroup
                  key={item.key}
                  groupMsgs={item.msgs}
                  mine={mine}
                  senderName={senderName}
                  showSenderName={ctx.kind === 'channel' && !mine}
                  nameLookup={nameLookup}
                  msgById={msgById}
                  menuFor={menuFor}
                  setMenuFor={setMenuFor}
                  reactingFor={reactingFor}
                  setReactingFor={setReactingFor}
                  myUserId={me?.id ?? ''}
                  peerReadSeq={ctx.kind === 'friend' ? peerLastReadSeq[ctx.convId]?.[ctx.targetId] ?? 0 : 0}
                  isPrivate={ctx.kind === 'friend'}
                  isLastMine={isLastMineMessage(item.msgs, me?.id, messages)}
                  onRecall={handleRecall}
                  onDelete={handleDelete}
                  onReply={(m) => setReplyingTo(m)}
                  onPin={handlePin}
                  onToggleReaction={handleToggleReaction}
                  editingId={editingId}
                  editingDraft={editingDraft}
                  onStartEdit={handleStartEdit}
                  onChangeEditingDraft={setEditingDraft}
                  onSaveEdit={handleSaveEdit}
                  onCancelEdit={handleCancelEdit}
                />
              );
            })}
          </div>
        )}
      </div>

      {(error || uploading) && (
        <div className="px-4 py-1.5 text-sm flex items-center gap-2">
          {uploading && (
            <span className="text-ink-3 flex items-center gap-1.5">
              <Loader2 size={14} className="animate-spin" /> {uploading}
            </span>
          )}
          {error && <span className="text-accent-red ml-2">{error}</span>}
        </div>
      )}

      {/* Typing indicator. Only shows when someone is typing in this conv. */}
      {Object.keys(typingPeers).length > 0 && (
        <div className="px-4 pt-1 pb-0 text-[11px] text-ink-3 flex items-center gap-1.5">
          <span className="inline-flex gap-0.5">
            <span className="w-1 h-1 rounded-full bg-ink-3 animate-bounce" style={{ animationDelay: '0ms' }} />
            <span className="w-1 h-1 rounded-full bg-ink-3 animate-bounce" style={{ animationDelay: '120ms' }} />
            <span className="w-1 h-1 rounded-full bg-ink-3 animate-bounce" style={{ animationDelay: '240ms' }} />
          </span>
          {Object.keys(typingPeers)
            .map((id) => nameLookup.get(id) ?? `用户 ${id}`)
            .slice(0, 3)
            .join(', ')}
          {Object.keys(typingPeers).length === 1 ? ' 正在输入…' : ' 正在输入…'}
        </div>
      )}

      <footer className="px-4 py-3 border-t border-bg-5/40 bg-bg-2/40 shrink-0">
        <div className="max-w-3xl mx-auto">
          {replyingTo && (
            <div className="mb-2 flex items-start gap-2 bg-bg-3 border border-bg-5/40 rounded-lg px-3 py-2 text-sm anim-fade">
              <Reply size={14} className="text-brand-300 mt-1 shrink-0" />
              <div className="min-w-0 flex-1">
                <div className="text-[11px] text-ink-3">
                  正在回复 <span className="text-brand-300">{nameLookup.get(replyingTo.senderId) ?? '某人'}</span>
                </div>
                <div className="truncate text-ink-2">{previewOf(replyingTo)}</div>
              </div>
              <button onClick={() => setReplyingTo(null)} className="btn-icon w-7 h-7" title="取消回复">
                <X size={14} />
              </button>
            </div>
          )}
          <div className="relative flex items-end gap-2 bg-bg-3 rounded-xl px-3 py-2 border border-bg-5/40 focus-within:border-brand-500 transition-colors">
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="btn-icon shrink-0"
              title="发图片 / 文件"
              disabled={!!uploading}
            >
              <Paperclip size={18} />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void handleUpload(f);
                e.target.value = '';
              }}
            />
            <div className="relative">
              <button
                type="button"
                onClick={() => setEmojiOpen((v) => !v)}
                className="btn-icon shrink-0"
                title="表情"
              >
                <Smile size={18} />
              </button>
              {emojiOpen && (
                <div className="absolute bottom-12 left-0 z-20">
                  <EmojiPicker
                    onPick={(e) => insertAtCursor(e)}
                    onClose={() => setEmojiOpen(false)}
                  />
                </div>
              )}
            </div>
            <textarea
              ref={textareaRef}
              value={text}
              onChange={(e) => {
                setText(e.target.value);
                if (e.target.value.trim().length > 0) sendTypingPing();
              }}
              onBlur={() => {
                if (!ctx || ctx.kind !== 'friend') return;
                wsClient.send('typing.stop', {
                  conversationId: ctx.convId,
                  recipientIds: [ctx.targetId],
                });
                lastTypingSentRef.current = 0;
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault();
                  handleSendText();
                }
                if (e.key === 'Escape' && replyingTo) {
                  e.preventDefault();
                  setReplyingTo(null);
                }
              }}
              rows={1}
              placeholder={`发消息到 ${ctx.title}（Enter 发送，Shift+Enter 换行）`}
              className="flex-1 bg-transparent outline-none resize-none text-ink-1 placeholder-ink-4 text-sm leading-6 py-1.5 max-h-40"
            />
            <button
              onClick={handleSendText}
              disabled={sending || !text.trim()}
              className="shrink-0 inline-flex items-center justify-center gap-1.5 px-3 h-9 rounded-lg bg-brand-500 hover:bg-brand-600 disabled:opacity-40 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
              title="发送"
            >
              {sending ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
              发送
            </button>
          </div>
          <div className="mt-1 px-1 text-[11px] text-ink-4 flex items-center gap-3 flex-wrap">
            <span className="flex items-center gap-1"><CornerDownLeft size={11} /> 发送</span>
            <span>Shift + ↵ 换行</span>
            <span>**粗** *斜* `代码`</span>
            <span>@用户名 提及</span>
            <span>悬停消息回复 / 撤回</span>
          </div>
        </div>
      </footer>
    </main>
  );
}

// ReplyQuote renders the gray "quoted message" strip above a reply.
// Two render paths:
//   - parent is live in the local cache → render real text + senderName
//     (most accurate; shows the latest edited body etc.)
//   - parent is gone (deleted / past retention) but the reply has the
//     server-embedded _replyToSnapshot in its own content → render
//     from the snapshot so the quote still makes sense.
// Both absent → muted "[原消息已不存在]" — the legitimate empty state.
function ReplyQuote({
  parent,
  snapshot,
  nameLookup,
}: {
  parent: ChatMessage | undefined;
  snapshot: { senderId: string; type: string; preview: string } | undefined;
  nameLookup: Map<string, string>;
}) {
  if (!parent && !snapshot) {
    return (
      <div className="text-xs text-ink-4 italic mb-1 pl-2 border-l-2 border-bg-5/60">
        原消息已不存在
      </div>
    );
  }
  // Prefer the live parent when we have it — gives accurate edit
  // state. Fall back to snapshot when the original is gone.
  const senderId = parent?.senderId ?? snapshot!.senderId;
  const senderName = nameLookup.get(senderId) ?? `用户 ${senderId}`;
  const preview = parent ? previewOf(parent) : snapshot!.preview;
  return (
    <div className="text-xs mb-1 pl-2 border-l-2 border-brand-500/60 max-w-full overflow-hidden">
      <div className="text-brand-300 font-medium truncate">{senderName}</div>
      <div className="text-ink-3 truncate">{preview}</div>
    </div>
  );
}

function MessageGroup({
  groupMsgs,
  mine,
  senderName,
  showSenderName,
  nameLookup,
  msgById,
  menuFor,
  setMenuFor,
  reactingFor,
  setReactingFor,
  myUserId,
  peerReadSeq,
  isPrivate,
  isLastMine,
  onRecall,
  onDelete,
  onReply,
  onPin,
  onToggleReaction,
  editingId,
  editingDraft,
  onStartEdit,
  onChangeEditingDraft,
  onSaveEdit,
  onCancelEdit,
}: {
  groupMsgs: ChatMessage[];
  mine: boolean;
  senderName: string;
  showSenderName: boolean;
  nameLookup: Map<string, string>;
  msgById: Map<string, ChatMessage>;
  menuFor: string | null;
  setMenuFor: (id: string | null) => void;
  reactingFor: string | null;
  setReactingFor: (id: string | null) => void;
  myUserId: string;
  peerReadSeq: number;
  isPrivate: boolean;
  isLastMine: string | null;
  onRecall: (m: ChatMessage) => void;
  onDelete: (m: ChatMessage) => void;
  onReply: (m: ChatMessage) => void;
  onPin: (m: ChatMessage) => void;
  onToggleReaction: (m: ChatMessage, emoji: string) => void;
  editingId: string | null;
  editingDraft: string;
  onStartEdit: (m: ChatMessage) => void;
  onChangeEditingDraft: (s: string) => void;
  onSaveEdit: (m: ChatMessage) => void;
  onCancelEdit: () => void;
}) {
  return (
    <div className={`flex gap-3 ${mine ? 'flex-row-reverse' : ''} px-1 py-1`}>
      <div className="shrink-0">
        <Avatar name={senderName || (mine ? 'me' : '?')} size={36} />
      </div>
      <div className={`min-w-0 max-w-[78%] flex-1 ${mine ? 'items-end' : 'items-start'} flex flex-col`}>
        {showSenderName && (
          <div className="text-xs text-ink-3 mb-0.5 ml-1">{senderName}</div>
        )}
        <div className={`flex flex-col gap-0.5 ${mine ? 'items-end' : 'items-start'} w-full`}>
          {groupMsgs.map((m, idx) => {
            const canRecall = mine && !m.isRecalled && withinRecallWindow(m.createdAt);
            // canEdit mirrors the server-side rules: own text message,
            // not recalled, within the 5-min edit window. Image/file
            // messages aren't editable; only "text" gives meaningful
            // rewrite semantics.
            const canEdit = mine && !m.isRecalled && m.type === 'text' && withinEditWindow(m.createdAt);
            const t = new Date(m.createdAt);
            const showTime = idx === groupMsgs.length - 1;
            const parent = m.replyTo ? msgById.get(String(m.replyTo)) : undefined;
            const isEditing = editingId === m.id;
            return (
              <div
                key={m.id}
                id={`msg-${m.id}`}
                className={`relative group max-w-full ${mine ? 'self-end' : 'self-start'}`}
              >
                <div
                  className={`relative inline-block px-3 py-2 rounded-2xl text-sm leading-relaxed shadow-soft transition-colors ${
                    mine
                      ? 'bg-brand-500 text-white rounded-br-md'
                      : 'bg-bg-3 text-ink-1 rounded-bl-md'
                  } ${m.isRecalled ? 'opacity-70' : ''}`}
                  onContextMenu={(e) => {
                    e.preventDefault();
                    setMenuFor(menuFor === m.id ? null : m.id);
                  }}
                >
                  {m.replyTo && (
                    <ReplyQuote
                      parent={parent}
                      snapshot={
                        // Embedded snapshot is shaped { senderId, type, preview }
                        // — the server attaches it at send time so the quote
                        // preview survives original-message deletion.
                        (m.content?._replyToSnapshot as
                          | { senderId: string; type: string; preview: string }
                          | undefined) ?? undefined
                      }
                      nameLookup={nameLookup}
                    />
                  )}
                  {isEditing ? (
                    <EditMessageInline
                      mine={mine}
                      value={editingDraft}
                      onChange={onChangeEditingDraft}
                      onSave={() => onSaveEdit(m)}
                      onCancel={onCancelEdit}
                    />
                  ) : (
                    <>
                      <MessageBody msg={m} nameLookup={nameLookup} />
                      {/* "(已编辑)" badge after the body — small, muted, no extra
                          layout. Only shows on actually-edited messages. */}
                      {m.editedAt && !m.isRecalled && (
                        <span
                          className={`ml-1 text-[10px] align-middle ${mine ? 'text-white/60' : 'text-ink-3'}`}
                          title={`编辑于 ${new Date(m.editedAt).toLocaleString()}${(m.editCount ?? 1) > 1 ? ` · 共编辑 ${m.editCount} 次` : ''}`}
                        >
                          (已编辑)
                        </span>
                      )}
                    </>
                  )}
                  {!isEditing && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setMenuFor(menuFor === m.id ? null : m.id);
                      }}
                      className={`absolute top-1/2 -translate-y-1/2 ${
                        mine ? 'left-0 -translate-x-[calc(100%+6px)]' : 'right-0 translate-x-[calc(100%+6px)]'
                      } opacity-0 group-hover:opacity-100 transition-opacity btn-icon w-7 h-7 bg-bg-3 hover:bg-bg-4 border border-bg-5/40`}
                      title="更多"
                    >
                      <MoreHorizontal size={14} />
                    </button>
                  )}
                </div>
                {/* Reactions strip */}
                {(m.reactions?.length ?? 0) > 0 && !m.isRecalled && (
                  <div className={`mt-1 flex flex-wrap gap-1 ${mine ? 'justify-end' : 'justify-start'}`}>
                    {m.reactions!.map((r) => {
                      const ireacted = (r.userIds ?? []).map(String).includes(myUserId);
                      return (
                        <button
                          key={r.emoji}
                          onClick={() => onToggleReaction(m, r.emoji)}
                          className={`text-xs px-1.5 py-0.5 rounded-full border transition-colors ${
                            ireacted
                              ? 'bg-brand-500/20 border-brand-500/60 text-brand-200'
                              : 'bg-bg-3 border-bg-5/40 text-ink-2 hover:bg-bg-4'
                          }`}
                          title={ireacted ? '取消反应' : '我也加一个'}
                        >
                          <span className="mr-1">{r.emoji}</span>
                          {r.count}
                        </button>
                      );
                    })}
                  </div>
                )}
                {showTime && (
                  <div className={`text-[10px] text-ink-4 mt-0.5 flex items-center gap-1.5 ${mine ? 'justify-end pr-1' : 'pl-1'}`}>
                    <span>{timeLabel(t)}</span>
                    {/* Private-chat read indicator on the latest own message */}
                    {mine && isPrivate && isLastMine === m.id && (
                      <span className={peerReadSeq >= m.seq ? 'text-brand-300' : 'text-ink-4'}>
                        · {peerReadSeq >= m.seq ? '已读' : '未读'}
                      </span>
                    )}
                  </div>
                )}
                {/* Reaction picker popover */}
                {reactingFor === m.id && (
                  <div
                    className={`absolute z-30 ${mine ? 'right-0' : 'left-0'} bottom-full mb-1`}
                  >
                    <EmojiPickerInline
                      onPick={(e) => { onToggleReaction(m, e); setReactingFor(null); }}
                      onClose={() => setReactingFor(null)}
                    />
                  </div>
                )}
                {menuFor === m.id && (
                  <div
                    className={`absolute z-20 mt-1 ${mine ? 'right-0' : 'left-0'} top-full bg-bg-3 border border-bg-5/40 rounded-lg shadow-pop overflow-hidden min-w-[140px]`}
                  >
                    {!m.isRecalled && (
                      <button
                        onClick={() => { setMenuFor(null); setReactingFor(m.id); }}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                      >
                        <SmilePlus size={14} /> 加表情
                      </button>
                    )}
                    {!m.isRecalled && (
                      <button
                        onClick={() => { setMenuFor(null); onReply(m); }}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                      >
                        <Reply size={14} /> 回复
                      </button>
                    )}
                    {!m.isRecalled && (
                      <button
                        onClick={() => onPin(m)}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                      >
                        <PinIcon size={14} /> 置顶
                      </button>
                    )}
                    {canEdit && (
                      <button
                        onClick={() => onStartEdit(m)}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                      >
                        <Pencil size={14} /> 编辑
                      </button>
                    )}
                    {canRecall && (
                      <button
                        onClick={() => onRecall(m)}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                      >
                        <RotateCcw size={14} /> 撤回
                      </button>
                    )}
                    {/* canDelete: own message, no recall-flag race, and within
                        the server's 30-day retention window. Server will
                        return 403 past this so we hide the menu item
                        client-side rather than offer a sure-to-fail click. */}
                    {mine && !m.isRecalled && (Date.now() - new Date(m.createdAt).getTime() < 30 * 24 * 60 * 60 * 1000) && (
                      <button
                        onClick={() => onDelete(m)}
                        className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4 text-accent-red"
                      >
                        <Trash2 size={14} /> 删除
                      </button>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// Wraps the EmojiPicker primitive so it can be used inline for reactions.
function EmojiPickerInline({ onPick, onClose }: { onPick: (e: string) => void; onClose: () => void }) {
  return <EmojiPicker onPick={onPick} onClose={onClose} />;
}

// EditMessageInline replaces a message bubble's body with a small
// auto-grown textarea + save/cancel buttons. ⌘/Ctrl+Enter saves, Esc
// cancels, plain Enter inserts a newline (so you can fix multi-line
// messages without losing structure). Auto-focused on mount.
function EditMessageInline({
  mine,
  value,
  onChange,
  onSave,
  onCancel,
}: {
  mine: boolean;
  value: string;
  onChange: (s: string) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const ta = ref.current;
    if (!ta) return;
    ta.focus();
    // Place caret at the end so the user can keep typing where they
    // last left off, not in the middle of the existing text.
    ta.setSelectionRange(ta.value.length, ta.value.length);
  }, []);
  // Auto-grow: clamp between 1 and ~8 visible lines based on scrollHeight.
  useEffect(() => {
    const ta = ref.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 220) + 'px';
  }, [value]);

  return (
    <div className="min-w-[200px] max-w-full">
      <textarea
        ref={ref}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault();
            onCancel();
          } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            onSave();
          }
        }}
        className={`w-full resize-none rounded-md px-2 py-1 text-sm leading-relaxed outline-none ring-1 ${
          mine ? 'bg-white/15 text-white placeholder-white/60 ring-white/30' : 'bg-bg-2 text-ink-1 ring-bg-5/60'
        } focus:ring-brand-500`}
        rows={1}
        placeholder="编辑消息内容…"
      />
      <div className={`mt-1 flex items-center gap-2 text-[11px] ${mine ? 'text-white/75' : 'text-ink-3'}`}>
        <button
          onClick={onSave}
          className={`px-2 py-0.5 rounded ${mine ? 'bg-white/20 hover:bg-white/30 text-white' : 'bg-brand-500 hover:bg-brand-600 text-white'}`}
        >
          保存
        </button>
        <button
          onClick={onCancel}
          className={`px-2 py-0.5 rounded ${mine ? 'hover:bg-white/15' : 'hover:bg-bg-4'}`}
        >
          取消
        </button>
        <span className="ml-1 opacity-70">⌘/Ctrl+Enter 保存 · Esc 取消</span>
      </div>
    </div>
  );
}
