import { create } from 'zustand';
import type { Channel, ChatMessage, Friend, Group, Pin, ReactionCount } from '@/types';
import { useSeqStore } from '@/sync/seqStore';

const MSG_CACHE_PREFIX = 'dfchat.msgs.';
const MSG_CACHE_LIMIT = 50;

export type ChatTarget =
  | { kind: 'friend'; id: string }
  | { kind: 'channel'; groupId: string; channelId: string };

export interface PendingJump {
  convId: string;
  messageId: string;
  seq: number;
}

interface ChatState {
  friends: Friend[];
  groups: Group[];
  channelsByGroup: Record<string, Channel[]>;
  activeTarget: ChatTarget | null;
  messagesByConv: Record<string, ChatMessage[]>;
  pinsByConv: Record<string, Pin[]>;
  // peerLastReadSeq[convId][userId] = highest seq that user has acknowledged.
  // For private chats, used to render "已读" on my own messages.
  peerLastReadSeq: Record<string, Record<string, number>>;
  mutedConvs: Set<string>;
  // Per-group "notification mode" (0=all 1=mention-only 2=muted), refreshed
  // when MembersPanel opens. Key = groupId. Used by Home WS listener to
  // decide whether to pop a toast / desktop notification for a new message.
  groupNotifyModes: Record<string, 0 | 1 | 2>;
  // True if recent unread messages in this conv @-mention the current user.
  // Numeric unread count comes from lastSeq - readSeq (seqStore); this flag
  // adds the "mention highlight" coloring that seq alone can't infer.
  mentionByConv: Record<string, boolean>;
  pendingJump: PendingJump | null;

  setFriends: (friends: Friend[]) => void;
  setGroups: (groups: Group[]) => void;
  setChannels: (groupId: string, channels: Channel[]) => void;
  setActiveTarget: (t: ChatTarget | null) => void;
  setMessages: (convId: string, msgs: ChatMessage[]) => void;
  mergeMessages: (convId: string, msgs: ChatMessage[]) => void;
  appendMessage: (msg: ChatMessage) => void;
  replaceMessage: (msg: ChatMessage) => void;
  removeMessage: (convId: string, messageId: string) => void;
  applyReactionUpdate: (convId: string, messageId: string, reactions: ReactionCount[]) => void;
  setPins: (convId: string, pins: Pin[]) => void;
  addPin: (convId: string, pin: Pin) => void;
  removePin: (convId: string, messageId: string) => void;
  setPeerRead: (convId: string, userId: string, seq: number) => void;
  setMuted: (convId: string, muted: boolean) => void;
  setMutedAll: (ids: string[]) => void;
  setGroupNotifyMode: (groupId: string, mode: 0 | 1 | 2) => void;
  bumpUnread: (convId: string, mention: boolean) => void;
  clearUnread: (convId: string) => void;
  setPendingJump: (j: PendingJump | null) => void;
  hydrateFromCache: () => void;
}

function cacheMessages(convId: string, msgs: ChatMessage[]) {
  try {
    const trimmed = msgs.slice(-MSG_CACHE_LIMIT);
    localStorage.setItem(MSG_CACHE_PREFIX + convId, JSON.stringify(trimmed));
  } catch { /* ignore quota */ }
}

// RetentionWindow mirrors the server's message.RetentionWindow constant
// in Go. Past this point the server can no longer recall / edit /
// delete a message — only the client's local archive holds it. Keep
// these two numbers in sync if the server changes its retention.
const RETENTION_WINDOW_MS = 30 * 24 * 60 * 60 * 1000;
function isPastAuthorityWindow(createdAt: string | number | Date): boolean {
  const t = typeof createdAt === 'number' ? createdAt : new Date(createdAt).getTime();
  if (!Number.isFinite(t)) return false; // bad input — don't reject
  return Date.now() - t > RETENTION_WINDOW_MS;
}

// archiveAppend / archiveRemove are write-through helpers around the
// encrypted local SQLite archive exposed by the Electron preload.
// They no-op in environments without the preload (vite browser dev
// mode, web embedding), letting the rest of the store work either
// way. Mapped to string-shaped fields because IPC strips non-JSON
// types (BigInt etc.).
function archiveAppend(msg: ChatMessage) {
  const a = (typeof window !== 'undefined' && window.dfchatArchive) || undefined;
  if (!a) return;
  void a.append({
    id: msg.id,
    conversationId: msg.conversationId,
    senderId: msg.senderId,
    type: msg.type,
    content: msg.content,
    seq: msg.seq,
    mentions: msg.mentions?.map(String),
    replyTo: msg.replyTo != null ? String(msg.replyTo) : undefined,
    isRecalled: !!msg.isRecalled,
    editedAt: msg.editedAt,
    editCount: msg.editCount,
    createdAt: msg.createdAt,
  });
}
function archiveRemove(messageId: string) {
  const a = (typeof window !== 'undefined' && window.dfchatArchive) || undefined;
  if (!a) return;
  void a.remove(messageId);
}

function loadAllCached(): Record<string, ChatMessage[]> {
  const out: Record<string, ChatMessage[]> = {};
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (!k || !k.startsWith(MSG_CACHE_PREFIX)) continue;
      const convId = k.slice(MSG_CACHE_PREFIX.length);
      const raw = localStorage.getItem(k);
      if (!raw) continue;
      const arr = JSON.parse(raw);
      if (Array.isArray(arr)) out[convId] = arr;
    }
  } catch { /* ignore */ }
  return out;
}

export const useChatStore = create<ChatState>((set) => ({
  friends: [],
  groups: [],
  channelsByGroup: {},
  activeTarget: null,
  messagesByConv: {},
  pinsByConv: {},
  peerLastReadSeq: {},
  mutedConvs: new Set<string>(),
  groupNotifyModes: {},
  mentionByConv: {},
  pendingJump: null,

  setFriends: (friends) => set({ friends }),
  setGroups: (groups) => set({ groups }),
  setChannels: (groupId, channels) =>
    set((s) => ({ channelsByGroup: { ...s.channelsByGroup, [groupId]: channels } })),
  setActiveTarget: (t) => set({ activeTarget: t }),
  // Note: clearing unread on conv-switch is done in ChatView (it already
  // computes ctx.convId — keeps this store free of cross-store coupling).

  setMessages: (convId, msgs) => {
    const ordered = [...msgs].reverse();
    cacheMessages(convId, ordered);
    // Mirror the just-fetched server window into the encrypted archive
    // so it's available offline + persists past the server's 30-day
    // retention. Each row goes through the existing write-through
    // helper which encrypts before persisting.
    for (const m of ordered) archiveAppend(m);
    set((s) => ({ messagesByConv: { ...s.messagesByConv, [convId]: ordered } }));
  },

  // mergeMessages adds msgs to the existing list, de-duping by id. Sort by seq.
  // Used for "jump-to-message" where we fetch a window around a target seq
  // and want to stitch it into whatever's already shown.
  mergeMessages: (convId, msgs) =>
    set((s) => {
      const existing = s.messagesByConv[convId] ?? [];
      const seen = new Set(existing.map((m) => m.id));
      const merged = existing.slice();
      for (const m of msgs) {
        if (seen.has(m.id)) continue;
        merged.push(m);
        seen.add(m.id);
        archiveAppend(m);
      }
      merged.sort((a, b) => a.seq - b.seq);
      cacheMessages(convId, merged);
      return { messagesByConv: { ...s.messagesByConv, [convId]: merged } };
    }),

  appendMessage: (msg) => {
    useSeqStore.getState().bumpLast(msg.conversationId, msg.seq);
    // Write-through to the encrypted local archive. The archive is
    // the long-term store; this in-memory map is just the hot cache.
    // Fire-and-forget — IPC errors are caught + logged in main.
    archiveAppend(msg);
    set((s) => {
      const existing = s.messagesByConv[msg.conversationId] ?? [];
      if (existing.some((m) => m.id === msg.id)) return s;
      const next = [...existing, msg];
      cacheMessages(msg.conversationId, next);
      return { messagesByConv: { ...s.messagesByConv, [msg.conversationId]: next } };
    });
  },

  replaceMessage: (msg) => {
    // Tamper-rejection gate: the server is only allowed to mutate
    // history within its 30-day authority window (RetentionWindow on
    // the server). If a chat.edit / chat.recall event arrives for a
    // message older than that, refuse to apply it locally — the
    // archive copy is the canonical truth past that horizon.
    if (isPastAuthorityWindow(msg.createdAt)) {
      // eslint-disable-next-line no-console
      console.warn('[archive] rejecting server mutation of expired message', {
        id: msg.id, createdAt: msg.createdAt,
      });
      return;
    }
    // Mirror the new state into the encrypted archive first so even
    // if the in-memory store hasn't been hydrated for this conv we
    // still record the updated row.
    archiveAppend(msg);
    set((s) => {
      const existing = s.messagesByConv[msg.conversationId];
      if (!existing) return s;
      const idx = existing.findIndex((m) => m.id === msg.id);
      if (idx < 0) return s;
      const next = existing.slice();
      next[idx] = msg;
      cacheMessages(msg.conversationId, next);
      return { messagesByConv: { ...s.messagesByConv, [msg.conversationId]: next } };
    });
  },

  // removeMessage drops a message from the in-memory view + the
  // localStorage cache. Used when the server hard-deletes a message
  // (chat.delete WS event) or when the author chose "delete" in the
  // context menu.
  //
  // The local archive is INTENTIONALLY NOT touched here when the
  // origin is a server event for a message older than 30 days — the
  // archive is the user's permanent copy and only the user can
  // request its deletion. For messages still within the server's
  // 30-day authority window, the archive does mirror the deletion
  // so re-hydration after restart doesn't bring the message back.
  removeMessage: (convId, messageId) => {
    // We don't know createdAt here (it isn't in the WS chat.delete
    // payload), so we look the message up in the in-memory view to
    // make the call. If we can't find it (already dropped), we
    // err on the safe side and KEEP the archive row — better to
    // preserve user data than discard it on ambiguous input.
    const current = (useChatStore.getState().messagesByConv[convId] ?? []).find((m) => m.id === messageId);
    if (current && !isPastAuthorityWindow(current.createdAt)) {
      archiveRemove(messageId);
    } else if (current) {
      // eslint-disable-next-line no-console
      console.warn('[archive] keeping archive row for expired-window delete', {
        id: messageId, createdAt: current.createdAt,
      });
    }
    set((s) => {
      const existing = s.messagesByConv[convId];
      if (!existing) return s;
      const next = existing.filter((m) => m.id !== messageId);
      if (next.length === existing.length) return s;
      cacheMessages(convId, next);
      return { messagesByConv: { ...s.messagesByConv, [convId]: next } };
    });
  },

  applyReactionUpdate: (convId, messageId, reactions) =>
    set((s) => {
      const arr = s.messagesByConv[convId];
      if (!arr) return s;
      const idx = arr.findIndex((m) => m.id === messageId);
      if (idx < 0) return s;
      const next = arr.slice();
      next[idx] = { ...next[idx], reactions };
      cacheMessages(convId, next);
      return { messagesByConv: { ...s.messagesByConv, [convId]: next } };
    }),

  setPins: (convId, pins) =>
    set((s) => ({ pinsByConv: { ...s.pinsByConv, [convId]: pins } })),

  addPin: (convId, pin) =>
    set((s) => {
      const existing = s.pinsByConv[convId] ?? [];
      if (existing.some((p) => p.messageId === pin.messageId)) return s;
      return { pinsByConv: { ...s.pinsByConv, [convId]: [pin, ...existing] } };
    }),

  removePin: (convId, messageId) =>
    set((s) => {
      const existing = s.pinsByConv[convId];
      if (!existing) return s;
      return { pinsByConv: { ...s.pinsByConv, [convId]: existing.filter((p) => p.messageId !== messageId) } };
    }),

  setPendingJump: (j) => set({ pendingJump: j }),

  setMuted: (convId, muted) =>
    set((s) => {
      const next = new Set(s.mutedConvs);
      if (muted) next.add(convId);
      else next.delete(convId);
      return { mutedConvs: next };
    }),

  setMutedAll: (ids) => set({ mutedConvs: new Set(ids) }),

  setGroupNotifyMode: (groupId: string, mode: 0 | 1 | 2) =>
    set((s) => ({ groupNotifyModes: { ...s.groupNotifyModes, [groupId]: mode } })),

  bumpUnread: (convId, mention) =>
    set((s) => mention
      ? { mentionByConv: { ...s.mentionByConv, [convId]: true } }
      : s),

  clearUnread: (convId) =>
    set((s) => {
      if (!(convId in s.mentionByConv)) return s;
      const m = { ...s.mentionByConv };
      delete m[convId];
      return { mentionByConv: m };
    }),

  setPeerRead: (convId, userId, seq) =>
    set((s) => {
      const cur = s.peerLastReadSeq[convId] ?? {};
      if ((cur[userId] ?? 0) >= seq) return s;
      return {
        peerLastReadSeq: {
          ...s.peerLastReadSeq,
          [convId]: { ...cur, [userId]: seq },
        },
      };
    }),

  hydrateFromCache: () => {
    const cached = loadAllCached();
    if (Object.keys(cached).length === 0) return;
    set((s) => ({ messagesByConv: { ...cached, ...s.messagesByConv } }));
  },
}));
