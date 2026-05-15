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
  pendingJump: PendingJump | null;

  setFriends: (friends: Friend[]) => void;
  setGroups: (groups: Group[]) => void;
  setChannels: (groupId: string, channels: Channel[]) => void;
  setActiveTarget: (t: ChatTarget | null) => void;
  setMessages: (convId: string, msgs: ChatMessage[]) => void;
  mergeMessages: (convId: string, msgs: ChatMessage[]) => void;
  appendMessage: (msg: ChatMessage) => void;
  replaceMessage: (msg: ChatMessage) => void;
  applyReactionUpdate: (convId: string, messageId: string, reactions: ReactionCount[]) => void;
  setPins: (convId: string, pins: Pin[]) => void;
  addPin: (convId: string, pin: Pin) => void;
  removePin: (convId: string, messageId: string) => void;
  setPeerRead: (convId: string, userId: string, seq: number) => void;
  setMuted: (convId: string, muted: boolean) => void;
  setMutedAll: (ids: string[]) => void;
  setGroupNotifyMode: (groupId: string, mode: 0 | 1 | 2) => void;
  setPendingJump: (j: PendingJump | null) => void;
  hydrateFromCache: () => void;
}

function cacheMessages(convId: string, msgs: ChatMessage[]) {
  try {
    const trimmed = msgs.slice(-MSG_CACHE_LIMIT);
    localStorage.setItem(MSG_CACHE_PREFIX + convId, JSON.stringify(trimmed));
  } catch { /* ignore quota */ }
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
  pendingJump: null,

  setFriends: (friends) => set({ friends }),
  setGroups: (groups) => set({ groups }),
  setChannels: (groupId, channels) =>
    set((s) => ({ channelsByGroup: { ...s.channelsByGroup, [groupId]: channels } })),
  setActiveTarget: (t) => set({ activeTarget: t }),

  setMessages: (convId, msgs) => {
    const ordered = [...msgs].reverse();
    cacheMessages(convId, ordered);
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
      }
      merged.sort((a, b) => a.seq - b.seq);
      cacheMessages(convId, merged);
      return { messagesByConv: { ...s.messagesByConv, [convId]: merged } };
    }),

  appendMessage: (msg) => {
    useSeqStore.getState().bumpLast(msg.conversationId, msg.seq);
    set((s) => {
      const existing = s.messagesByConv[msg.conversationId] ?? [];
      if (existing.some((m) => m.id === msg.id)) return s;
      const next = [...existing, msg];
      cacheMessages(msg.conversationId, next);
      return { messagesByConv: { ...s.messagesByConv, [msg.conversationId]: next } };
    });
  },

  replaceMessage: (msg) =>
    set((s) => {
      const existing = s.messagesByConv[msg.conversationId];
      if (!existing) return s;
      const idx = existing.findIndex((m) => m.id === msg.id);
      if (idx < 0) return s;
      const next = existing.slice();
      next[idx] = msg;
      cacheMessages(msg.conversationId, next);
      return { messagesByConv: { ...s.messagesByConv, [msg.conversationId]: next } };
    }),

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
