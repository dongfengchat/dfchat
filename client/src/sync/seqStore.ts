// Per-conversation seq tracking, persisted to localStorage so sync survives
// app restarts. Modeled as a Zustand store so sidebar badges re-render when
// new messages arrive.
//
//   lastSeq[convId]     — highest seq the client has actually received.
//   lastReadSeq[convId] — highest seq the user has acknowledged (opened past).

import { create } from 'zustand';

const LAST_SEQ_KEY = 'dfchat.lastSeq';
const LAST_READ_KEY = 'dfchat.lastReadSeq';

type SeqMap = Record<string, number>;

function load(key: string): SeqMap {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return {};
    const obj = JSON.parse(raw);
    return typeof obj === 'object' && obj ? obj : {};
  } catch {
    return {};
  }
}

function save(key: string, map: SeqMap) {
  try {
    localStorage.setItem(key, JSON.stringify(map));
  } catch {
    // quota / private mode — best effort.
  }
}

interface SeqStore {
  last: SeqMap;
  read: SeqMap;
  bumpLast: (convId: string, seq: number) => void;
  markRead: (convId: string, seq: number) => void;
  clearAll: () => void;
}

export const useSeqStore = create<SeqStore>((set, get) => ({
  last: load(LAST_SEQ_KEY),
  read: load(LAST_READ_KEY),

  bumpLast: (convId, seq) => {
    const current = get().last[convId] ?? 0;
    if (seq > current) {
      const nextLast = { ...get().last, [convId]: seq };
      save(LAST_SEQ_KEY, nextLast);
      set({ last: nextLast });
    }
  },

  markRead: (convId, seq) => {
    const current = get().read[convId] ?? 0;
    if (seq > current) {
      const nextRead = { ...get().read, [convId]: seq };
      save(LAST_READ_KEY, nextRead);
      set({ read: nextRead });
    }
  },

  clearAll: () => {
    try {
      localStorage.removeItem(LAST_SEQ_KEY);
      localStorage.removeItem(LAST_READ_KEY);
    } catch {
      // ignore
    }
    set({ last: {}, read: {} });
  },
}));

export function unreadOf(convId: string): number {
  const { last, read } = useSeqStore.getState();
  const l = last[convId] ?? 0;
  const r = read[convId] ?? 0;
  return Math.max(0, l - r);
}
