import { useEffect, useMemo, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  Hash,
  Inbox,
  LogOut,
  Plus,
  RadioTower,
  Search,
  Settings,
  ShieldCheck,
  UserPlus,
  Users,
} from 'lucide-react';
import FriendRequestsModal from './FriendRequestsModal';
import {
  addFriend,
  channelConvId,
  createChannel,
  createGroup,
  joinGroup,
  listChannels,
  listFriends,
  listFriendRequests,
  listGroups,
  privateConvId,
} from '@/api/client';
import { useChatStore, type ChatTarget } from '@/store/chatStore';
import { useUserStore } from '@/store/userStore';
import { useSeqStore } from '@/sync/seqStore';
import Avatar from './ui/Avatar';
import Modal from './ui/Modal';
import { toast } from './ui/Toast';

function targetEquals(a: ChatTarget | null, b: ChatTarget): boolean {
  if (!a || a.kind !== b.kind) return false;
  if (a.kind === 'friend' && b.kind === 'friend') return a.id === b.id;
  if (a.kind === 'channel' && b.kind === 'channel') return a.channelId === b.channelId;
  return false;
}

function UnreadBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span className="ml-auto shrink-0 min-w-[1.25rem] h-5 px-1.5 rounded-full bg-accent-red text-white text-[11px] font-medium flex items-center justify-center">
      {count > 99 ? '99+' : count}
    </span>
  );
}

type ModalKind = null | 'add-friend' | 'create-group' | 'join-group' | { kind: 'create-channel'; groupId: string };

export default function FriendSidebar({
  onLogout,
  onOpenAdmin,
  onOpenSearch,
  onOpenSettings,
  onOpenLive,
}: {
  onLogout: () => void;
  onOpenAdmin: () => void;
  onOpenSearch?: () => void;
  onOpenSettings?: () => void;
  onOpenLive?: () => void;
}) {
  const user = useUserStore((s) => s.user);
  const friends = useChatStore((s) => s.friends);
  const groups = useChatStore((s) => s.groups);
  const channelsByGroup = useChatStore((s) => s.channelsByGroup);
  const setFriends = useChatStore((s) => s.setFriends);
  const setGroups = useChatStore((s) => s.setGroups);
  const setChannels = useChatStore((s) => s.setChannels);
  const activeTarget = useChatStore((s) => s.activeTarget);
  const setActiveTarget = useChatStore((s) => s.setActiveTarget);
  const lastSeq = useSeqStore((s) => s.last);
  const readSeq = useSeqStore((s) => s.read);

  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [modal, setModal] = useState<ModalKind>(null);
  const [search, setSearch] = useState('');
  const [incomingCount, setIncomingCount] = useState(0);
  const [requestsOpen, setRequestsOpen] = useState(false);

  // Poll incoming request count on mount + every 60s. WS friend.request also
  // bumps it (wired in Home.tsx via window event).
  useEffect(() => {
    let alive = true;
    async function refresh() {
      try {
        const r = await listFriendRequests();
        if (alive) setIncomingCount(r.incoming.length);
      } catch { /* ignore */ }
    }
    refresh();
    const id = window.setInterval(refresh, 60000);
    const onBump = () => refresh();
    window.addEventListener('dfchat.friend-request', onBump);
    return () => {
      alive = false;
      clearInterval(id);
      window.removeEventListener('dfchat.friend-request', onBump);
    };
  }, []);

  // Lazy-load channels when a group is first expanded.
  useEffect(() => {
    for (const g of groups) {
      if (expanded[g.id] && !channelsByGroup[g.id]) {
        listChannels(g.id).then((chs) => setChannels(g.id, chs)).catch(() => {});
      }
    }
  }, [expanded, groups, channelsByGroup, setChannels]);

  function unread(convId: string): number {
    return Math.max(0, (lastSeq[convId] ?? 0) - (readSeq[convId] ?? 0));
  }

  function groupUnread(groupId: string): number {
    const chs = channelsByGroup[groupId] || [];
    return chs.reduce((sum, c) => sum + unread(channelConvId(c.id)), 0);
  }

  const totalUnread = useMemo(() => {
    let n = 0;
    if (user) friends.forEach((f) => (n += unread(privateConvId(user.id, f.id))));
    groups.forEach((g) => (n += groupUnread(g.id)));
    return n;
  }, [friends, groups, lastSeq, readSeq, channelsByGroup, user]);

  // Filtered for the search box.
  const q = search.trim().toLowerCase();
  const filteredFriends = q
    ? friends.filter((f) => (f.nickname || f.username).toLowerCase().includes(q) || f.username.toLowerCase().includes(q))
    : friends;
  const filteredGroups = q ? groups.filter((g) => g.name.toLowerCase().includes(q)) : groups;

  return (
    <aside className="w-72 bg-bg-2 flex flex-col border-r border-bg-5/40">
      {/* Profile header */}
      <div className="px-4 py-3.5 border-b border-bg-5/40 flex items-center gap-3">
        <Avatar name={user?.nickname ?? '?'} online size={36} />
        <div className="flex-1 min-w-0">
          <div className="font-medium truncate text-ink-1">{user?.nickname}</div>
          <div className="text-xs text-ink-3 truncate">@{user?.username}</div>
        </div>
        {onOpenLive && (
          <button
            onClick={onOpenLive}
            className="btn-icon"
            title="直播"
            aria-label="直播"
          >
            <RadioTower size={18} className="text-brand-300" />
          </button>
        )}
        {user?.isAdmin && (
          <button
            onClick={onOpenAdmin}
            className="btn-icon"
            title="管理后台"
            aria-label="管理后台"
          >
            <ShieldCheck size={18} className="text-accent-amber" />
          </button>
        )}
        <button
          onClick={() => onOpenSettings?.()}
          className="btn-icon"
          title="设置"
          aria-label="设置"
        >
          <Settings size={18} />
        </button>
        <button onClick={onLogout} className="btn-icon" title="退出登录" aria-label="退出">
          <LogOut size={18} />
        </button>
      </div>

      {/* Sidebar quick filter + global search */}
      <div className="px-3 pt-3 pb-2 space-y-2">
        <div className="relative">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-4" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="过滤好友 / 群组"
            className="input pl-9 py-2 text-sm"
          />
        </div>
        {onOpenSearch && (
          <button
            onClick={onOpenSearch}
            className="w-full flex items-center gap-2 px-3 py-1.5 rounded-lg bg-bg-3 hover:bg-bg-4 text-xs text-ink-3 transition-colors"
          >
            <Search size={12} />
            <span className="flex-1 text-left">搜索消息历史</span>
            <kbd className="text-[10px] bg-bg-1/60 border border-bg-5/40 px-1.5 py-0.5 rounded">⌘K</kbd>
          </button>
        )}
        {totalUnread > 0 && (
          <div className="px-1 text-xs text-ink-3">
            共有 <span className="text-accent-red font-medium">{totalUnread}</span> 条未读
          </div>
        )}
      </div>

      <div className="flex-1 overflow-y-auto pb-2">
        {/* Friends section */}
        <SectionHeader
          icon={<Users size={13} />}
          title="好友"
          action={
            <button
              onClick={() => setModal('add-friend')}
              className="btn-icon w-6 h-6"
              title="添加好友"
              aria-label="添加好友"
            >
              <UserPlus size={14} />
            </button>
          }
        />
        <button
          onClick={() => setRequestsOpen(true)}
          className="w-full px-3 py-2 mx-1 mb-0.5 rounded-lg flex items-center gap-3 text-left hover:bg-bg-3 text-ink-2"
          style={{ width: 'calc(100% - 0.5rem)' }}
        >
          <span className="w-8 h-8 rounded-lg bg-bg-3 flex items-center justify-center"><Inbox size={16} /></span>
          <span className="flex-1 text-sm">好友请求</span>
          <UnreadBadge count={incomingCount} />
        </button>
        <div>
          {filteredFriends.length === 0 && (
            <div className="px-4 py-2 text-xs text-ink-4">{q ? '没有匹配的好友' : '点上方 + 添加好友'}</div>
          )}
          {filteredFriends.map((f) => {
            const t: ChatTarget = { kind: 'friend', id: f.id };
            const u = user ? unread(privateConvId(user.id, f.id)) : 0;
            const active = targetEquals(activeTarget, t);
            return (
              <button
                key={f.id}
                onClick={() => setActiveTarget(t)}
                className={`group w-full px-3 py-2 mx-1 mb-0.5 rounded-lg flex items-center gap-3 text-left transition-colors ${
                  active ? 'bg-brand-500/15 text-ink-1' : 'hover:bg-bg-3 text-ink-2'
                }`}
                style={{ width: 'calc(100% - 0.5rem)' }}
              >
                <Avatar name={f.nickname || f.username} size={32} online={!!f.isOnline} />
                <div className="min-w-0 flex-1">
                  <div className={`font-medium truncate ${active ? 'text-ink-1' : ''}`}>
                    {f.nickname || f.username}
                  </div>
                  <div className="text-[11px] text-ink-4 truncate">
                    {f.isOnline ? <span className="text-accent-green">在线</span> : `@${f.username}`}
                  </div>
                </div>
                <UnreadBadge count={u} />
              </button>
            );
          })}
        </div>

        {/* Groups section */}
        <SectionHeader
          icon={<Hash size={13} />}
          title="群组"
          action={
            <div className="flex gap-1">
              <button
                onClick={() => setModal('create-group')}
                className="btn-icon w-6 h-6"
                title="创建群"
                aria-label="创建群"
              >
                <Plus size={14} />
              </button>
              <button
                onClick={() => setModal('join-group')}
                className="text-[11px] text-ink-3 hover:text-ink-1 px-1.5 h-6 rounded hover:bg-bg-3"
                title="通过邀请码加入"
              >
                加入
              </button>
            </div>
          }
        />
        <div>
          {filteredGroups.length === 0 && (
            <div className="px-4 py-2 text-xs text-ink-4">{q ? '没有匹配的群组' : '还没加入群'}</div>
          )}
          {filteredGroups.map((g) => {
            const isExpanded = !!expanded[g.id];
            const chs = channelsByGroup[g.id] || [];
            const gUnread = isExpanded ? 0 : groupUnread(g.id);
            return (
              <div key={g.id} className="px-1 mb-0.5">
                <button
                  onClick={() => setExpanded((s) => ({ ...s, [g.id]: !s[g.id] }))}
                  className="w-full px-3 py-2 rounded-lg hover:bg-bg-3 flex items-center gap-3 text-left text-ink-2"
                >
                  <span className="text-ink-4">
                    {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                  </span>
                  <Avatar name={g.name} size={28} />
                  <div className="min-w-0 flex-1">
                    <div className="font-medium truncate text-ink-1">{g.name}</div>
                    <div className="text-[11px] text-ink-4 truncate">{g.memberCount} 人</div>
                  </div>
                  <UnreadBadge count={gUnread} />
                </button>
                {isExpanded && (
                  <div className="mt-0.5 ml-7 pl-2 border-l border-bg-5/40">
                    {chs.length === 0 && (
                      <div className="py-1 text-xs text-ink-4">加载中…</div>
                    )}
                    {chs.map((c) => {
                      const t: ChatTarget = { kind: 'channel', groupId: g.id, channelId: c.id };
                      const u = unread(channelConvId(c.id));
                      const active = targetEquals(activeTarget, t);
                      return (
                        <button
                          key={c.id}
                          onClick={() => setActiveTarget(t)}
                          className={`w-full py-1.5 pl-2 pr-2 flex items-center gap-2 text-sm text-left rounded transition-colors ${
                            active ? 'bg-brand-500/15 text-ink-1' : 'text-ink-3 hover:bg-bg-3 hover:text-ink-1'
                          }`}
                        >
                          <Hash size={14} className="text-ink-4" />
                          <span className="truncate flex-1">{c.name}</span>
                          <UnreadBadge count={u} />
                        </button>
                      );
                    })}
                    {user?.id === g.ownerId && (
                      <button
                        onClick={() => setModal({ kind: 'create-channel', groupId: g.id })}
                        className="w-full py-1 pl-2 text-xs text-ink-4 hover:text-ink-2 text-left flex items-center gap-1.5"
                      >
                        <Plus size={12} /> 新建频道
                      </button>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      <FriendRequestsModal
        open={requestsOpen}
        onClose={() => {
          setRequestsOpen(false);
          // Refresh count after the user has had a chance to act.
          listFriendRequests().then((r) => setIncomingCount(r.incoming.length)).catch(() => {});
        }}
      />

      <SidebarModals
        modal={modal}
        onClose={() => setModal(null)}
        onAddFriend={async (username) => {
          await addFriend(username);
          // Requests are now pending until recipient accepts. Refresh friends
          // anyway in case the request auto-accepted via reciprocal pending.
          setFriends(await listFriends());
          toast(`已发送好友请求给 ${username}`, 'success');
        }}
        onCreateGroup={async (name) => {
          const g = await createGroup(name);
          setGroups(await listGroups());
          toast(`已创建群「${g.name}」，邀请码 ${g.inviteCode}`, 'success');
        }}
        onJoinGroup={async (codeOrLink) => {
          // Accept either a bare invite code or a full URL/deep-link from
          // friends. We extract the trailing token; otherwise treat the
          // whole input as the code (after trimming whitespace).
          const m = codeOrLink.match(/[/=]([A-Za-z0-9]{6,})\s*$/);
          const code = (m ? m[1] : codeOrLink).trim();
          const g = await joinGroup(code);
          setGroups(await listGroups());
          toast(`欢迎加入「${g.name}」！可以在 #general 频道开始聊天`, 'success');
        }}
        onCreateChannel={async (groupId, name) => {
          const ch = await createChannel(groupId, name);
          const fresh = await listChannels(groupId);
          setChannels(groupId, fresh);
          toast(`已创建频道 #${ch.name}`, 'success');
        }}
      />
    </aside>
  );
}

function SectionHeader({ icon, title, action }: { icon: React.ReactNode; title: string; action?: React.ReactNode }) {
  return (
    <div className="px-4 pt-3 pb-1 flex items-center justify-between">
      <div className="text-[11px] uppercase tracking-wider text-ink-4 font-medium flex items-center gap-1.5">
        <span className="text-ink-4">{icon}</span>
        {title}
      </div>
      {action}
    </div>
  );
}

function SidebarModals({
  modal,
  onClose,
  onAddFriend,
  onCreateGroup,
  onJoinGroup,
  onCreateChannel,
}: {
  modal: ModalKind;
  onClose: () => void;
  onAddFriend: (username: string) => Promise<void>;
  onCreateGroup: (name: string) => Promise<void>;
  onJoinGroup: (code: string) => Promise<void>;
  onCreateChannel: (groupId: string, name: string) => Promise<void>;
}) {
  const [input, setInput] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setInput('');
    setError(null);
    setBusy(false);
  }, [modal]);

  if (!modal) return null;

  const cfg =
    modal === 'add-friend'
      ? { title: '添加好友', placeholder: '对方用户名', confirm: '添加' }
      : modal === 'create-group'
      ? { title: '创建群组', placeholder: '群名称', confirm: '创建' }
      : modal === 'join-group'
      ? { title: '加入群组', placeholder: '邀请码 或 完整链接', confirm: '加入' }
      : { title: '新建频道', placeholder: '频道名称（如 random）', confirm: '创建' };

  async function submit() {
    if (!input.trim()) return;
    setError(null);
    setBusy(true);
    try {
      if (modal === 'add-friend') await onAddFriend(input.trim());
      else if (modal === 'create-group') await onCreateGroup(input.trim());
      else if (modal === 'join-group') await onJoinGroup(input.trim());
      else if (modal !== null && typeof modal === 'object' && modal.kind === 'create-channel') await onCreateChannel(modal.groupId, input.trim());
      onClose();
    } catch (err: any) {
      setError(err.message ?? '操作失败');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={cfg.title}
      footer={
        <>
          <button onClick={onClose} className="btn-secondary">取消</button>
          <button onClick={submit} disabled={busy || !input.trim()} className="btn-primary">
            {busy ? '处理中…' : cfg.confirm}
          </button>
        </>
      }
    >
      <input
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && submit()}
        placeholder={cfg.placeholder}
        className="input"
      />
      {error && <div className="text-sm text-accent-red">{error}</div>}
    </Modal>
  );
}
