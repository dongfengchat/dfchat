import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Hash, Loader2, Search, User as UserIcon, X } from 'lucide-react';
import { channelConvId, privateConvId, searchMessages, type SearchHit } from '@/api/client';
import { useChatStore, type ChatTarget } from '@/store/chatStore';
import { useUserStore } from '@/store/userStore';

function highlight(text: string, term: string): React.ReactNode {
  if (!term) return text;
  const lower = text.toLowerCase();
  const t = term.toLowerCase();
  const out: React.ReactNode[] = [];
  let last = 0;
  let i = lower.indexOf(t);
  let k = 0;
  while (i >= 0) {
    if (i > last) out.push(text.slice(last, i));
    out.push(
      <mark key={k++} className="bg-amber-500/30 text-amber-200 rounded px-0.5">
        {text.slice(i, i + term.length)}
      </mark>,
    );
    last = i + term.length;
    i = lower.indexOf(t, last);
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function timeShort(s: string): string {
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return '';
  const today = new Date();
  if (d.getFullYear() === today.getFullYear() && d.getMonth() === today.getMonth() && d.getDate() === today.getDate()) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
  }
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

export default function SearchModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const me = useUserStore((s) => s.user);
  const friends = useChatStore((s) => s.friends);
  const groups = useChatStore((s) => s.groups);
  const channelsByGroup = useChatStore((s) => s.channelsByGroup);
  const setActiveTarget = useChatStore((s) => s.setActiveTarget);

  const [q, setQ] = useState('');
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [busy, setBusy] = useState(false);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // Debounce query — searches kick off ~200ms after typing stops.
  useEffect(() => {
    if (!open) return;
    if (!q.trim()) {
      setHits([]);
      setBusy(false);
      return;
    }
    setBusy(true);
    const id = window.setTimeout(async () => {
      try {
        const list = await searchMessages(q);
        setHits(list);
        setActive(0);
      } catch {
        setHits([]);
      } finally {
        setBusy(false);
      }
    }, 220);
    return () => clearTimeout(id);
  }, [q, open]);

  // Reset on open / focus input.
  useEffect(() => {
    if (!open) return;
    setQ('');
    setHits([]);
    setActive(0);
    setTimeout(() => inputRef.current?.focus(), 0);
  }, [open]);

  // Resolve a conv id → ChatTarget the sidebar can select.
  function convToTarget(convId: string): ChatTarget | null {
    if (convId.startsWith('p_')) {
      const [, a, b] = convId.split('_');
      const meId = me?.id;
      if (!meId) return null;
      return { kind: 'friend', id: a === meId ? b : a };
    }
    if (convId.startsWith('c_')) {
      const channelId = convId.slice(2);
      for (const [gid, chs] of Object.entries(channelsByGroup)) {
        if (chs.some((c) => c.id === channelId)) return { kind: 'channel', groupId: gid, channelId };
      }
      // Fallback: derive from groups if channels-by-group not loaded yet.
      for (const g of groups) return { kind: 'channel', groupId: g.id, channelId };
    }
    return null;
  }

  function openHit(h: SearchHit) {
    const t = convToTarget(h.conversationId);
    if (t) {
      setActiveTarget(t);
      // Stash the message id + seq so ChatView can fetch around it and scroll.
      useChatStore.getState().setPendingJump({
        convId: h.conversationId,
        messageId: h.id,
        seq: h.seq,
      });
    }
    onClose();
  }

  // Build a small name/title lookup for nicer hit rows.
  const titleFor = useMemo(() => {
    const map = new Map<string, { label: string; icon: React.ReactNode }>();
    for (const f of friends) {
      if (!me) break;
      map.set(privateConvId(me.id, f.id), {
        label: f.nickname || f.username,
        icon: <UserIcon size={14} className="text-ink-3" />,
      });
    }
    for (const g of groups) {
      for (const c of channelsByGroup[g.id] || []) {
        map.set(channelConvId(c.id), {
          label: `${g.name} · #${c.name}`,
          icon: <Hash size={14} className="text-ink-3" />,
        });
      }
    }
    return map;
  }, [me, friends, groups, channelsByGroup]);

  const senderName = useMemo(() => {
    const map = new Map<string, string>();
    if (me) map.set(me.id, me.nickname || me.username);
    friends.forEach((f) => map.set(f.id, f.nickname || f.username));
    return map;
  }, [me, friends]);

  function onKey(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((a) => Math.min(hits.length - 1, a + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((a) => Math.max(0, a - 1));
    } else if (e.key === 'Enter' && hits[active]) {
      e.preventDefault();
      openHit(hits[active]);
    } else if (e.key === 'Escape') {
      onClose();
    }
  }

  if (!open) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/55 backdrop-blur-sm pt-24 anim-fade"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="card w-full max-w-xl anim-scale shadow-pop overflow-hidden">
        <div className="px-4 py-3 border-b border-bg-5/40 flex items-center gap-2">
          <Search size={16} className="text-ink-3 shrink-0" />
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onKey}
            placeholder="搜索消息（支持中英文）"
            className="flex-1 bg-transparent outline-none text-ink-1 placeholder-ink-4 text-sm"
          />
          {busy && <Loader2 size={14} className="animate-spin text-ink-3" />}
          <button onClick={onClose} className="btn-icon w-7 h-7" aria-label="关闭">
            <X size={14} />
          </button>
        </div>

        <div className="max-h-96 overflow-y-auto">
          {q.trim() === '' && (
            <div className="px-4 py-6 text-sm text-ink-4 text-center">
              输入关键词搜索全部好友 & 频道的历史消息
              <div className="mt-2 text-[11px]">
                <kbd className="bg-bg-3 border border-bg-5/40 rounded px-1.5 py-0.5">↑</kbd>
                <kbd className="bg-bg-3 border border-bg-5/40 rounded px-1.5 py-0.5 ml-1">↓</kbd> 选择
                <span className="mx-2">·</span>
                <kbd className="bg-bg-3 border border-bg-5/40 rounded px-1.5 py-0.5">↵</kbd> 打开
                <span className="mx-2">·</span>
                <kbd className="bg-bg-3 border border-bg-5/40 rounded px-1.5 py-0.5">Esc</kbd> 关闭
              </div>
            </div>
          )}
          {q.trim() !== '' && !busy && hits.length === 0 && (
            <div className="px-4 py-6 text-sm text-ink-4 text-center">没有匹配的消息</div>
          )}
          {hits.map((h, i) => {
            const meta = titleFor.get(h.conversationId);
            const sName = senderName.get(h.senderId) ?? `用户 ${h.senderId}`;
            const isActive = i === active;
            return (
              <button
                key={h.id}
                onMouseEnter={() => setActive(i)}
                onClick={() => openHit(h)}
                className={`w-full px-4 py-2.5 text-left border-b border-bg-5/20 last:border-0 flex items-start gap-3 transition-colors ${
                  isActive ? 'bg-bg-3' : 'hover:bg-bg-3/60'
                }`}
              >
                <span className="mt-0.5">{meta?.icon ?? <UserIcon size={14} className="text-ink-3" />}</span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2">
                    <span className="text-xs text-ink-3 truncate">{meta?.label ?? h.conversationId}</span>
                    <span className="text-ink-4 text-xs">·</span>
                    <span className="text-xs text-ink-3 truncate">{sName}</span>
                    <span className="ml-auto text-[11px] text-ink-4 shrink-0">{timeShort(h.createdAt)}</span>
                  </div>
                  <div className="text-sm text-ink-1 mt-0.5 truncate">
                    {highlight(h.text, q.trim())}
                  </div>
                </div>
              </button>
            );
          })}
        </div>
      </div>
    </div>,
    document.body,
  );
}
