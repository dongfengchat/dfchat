import { useEffect, useMemo, useState } from 'react';
import { Bell, Crown, DoorOpen, MoreVertical, Search, Shield, UserMinus, UserPlus2 } from 'lucide-react';
import {
  getGroupNotifyMode,
  kickMember,
  leaveGroup,
  listGroupMembers,
  setGroupNotifyMode,
  setMemberRole,
} from '@/api/client';
import { useUserStore } from '@/store/userStore';
import { useChatStore } from '@/store/chatStore';
import Avatar from './ui/Avatar';
import Modal from './ui/Modal';
import { toast } from './ui/Toast';
import type { GroupMember } from '@/types';

interface MembersPanelProps {
  open: boolean;
  onClose: () => void;
  groupId: string;
  groupName: string;
  ownerId: string;
}

function roleLabel(role: number) {
  if (role === 2) return { text: '群主', icon: <Crown size={11} />, color: 'text-amber-300 bg-amber-500/15' };
  if (role === 1) return { text: '管理员', icon: <Shield size={11} />, color: 'text-brand-300 bg-brand-500/15' };
  return { text: '成员', icon: null as React.ReactNode, color: 'text-ink-3 bg-bg-3' };
}

export default function MembersPanel({ open, onClose, groupId, groupName, ownerId }: MembersPanelProps) {
  const me = useUserStore((s) => s.user);
  const [members, setMembers] = useState<GroupMember[] | null>(null);
  const [openMenuFor, setOpenMenuFor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [notifyMode, setNotifyMode] = useState<0 | 1 | 2>(0);
  const storeSetGroupNotifyMode = useChatStore((s) => s.setGroupNotifyMode);
  useEffect(() => {
    if (!open) return;
    getGroupNotifyMode(groupId)
      .then((m) => {
        const v = (m as 0 | 1 | 2) ?? 0;
        setNotifyMode(v);
        storeSetGroupNotifyMode(groupId, v);
      })
      .catch(() => {});
  }, [open, groupId, storeSetGroupNotifyMode]);
  async function changeNotifyMode(m: 0 | 1 | 2) {
    setNotifyMode(m);
    storeSetGroupNotifyMode(groupId, m);
    try {
      await setGroupNotifyMode(groupId, m);
      toast(m === 0 ? '已设为全部消息' : m === 1 ? '已设为只看 @ 我' : '已设为免打扰', 'success');
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }
  const visibleMembers = useMemo(() => {
    if (!members) return null;
    if (!search.trim()) return members;
    const q = search.trim().toLowerCase();
    return members.filter((m) =>
      m.username.toLowerCase().includes(q) ||
      (m.nickname || '').toLowerCase().includes(q),
    );
  }, [members, search]);

  async function selfLeave() {
    if (!confirm(`确定退出群组「${groupName}」？退出后无法看到本群历史消息。`)) return;
    try {
      await leaveGroup(groupId);
      toast('已退出群组', 'success');
      onClose();
      // Reload to refresh the sidebar (avoids stale group entry).
      setTimeout(() => window.location.reload(), 300);
    } catch (err: any) {
      toast(err.message ?? '退出失败', 'error');
    }
  }

  async function load() {
    setError(null);
    try {
      setMembers(await listGroupMembers(groupId));
    } catch (err: any) {
      setError(err.message ?? '加载失败');
    }
  }

  useEffect(() => {
    if (open) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, groupId]);

  if (!open) return null;

  const myRole = members?.find((m) => m.userId === me?.id)?.role ?? 0;
  const iAmOwner = me?.id === ownerId;

  async function changeRole(target: GroupMember, role: 0 | 1) {
    setOpenMenuFor(null);
    try {
      await setMemberRole(groupId, target.userId, role);
      toast(role === 1 ? '已升为管理员' : '已降为普通成员', 'success');
      await load();
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }

  async function kick(target: GroupMember) {
    setOpenMenuFor(null);
    if (!confirm(`确定将 ${target.nickname || target.username} 踢出群组吗？`)) return;
    try {
      await kickMember(groupId, target.userId);
      toast('已踢出', 'success');
      await load();
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }

  return (
    <Modal open onClose={onClose} title={`${groupName} · 成员`} size="md">
      {error && <div className="text-sm text-accent-red">{error}</div>}

      {/* Notify mode pill row */}
      <div className="flex items-center gap-2 mb-3 text-xs">
        <Bell size={12} className="text-ink-3" />
        <span className="text-ink-3">我的通知</span>
        {([
          { v: 0, label: '全部' },
          { v: 1, label: '仅 @ 我' },
          { v: 2, label: '免打扰' },
        ] as const).map((opt) => (
          <button
            key={opt.v}
            onClick={() => changeNotifyMode(opt.v)}
            className={`px-2.5 py-0.5 rounded-full transition-colors ${
              notifyMode === opt.v ? 'bg-brand-500 text-white' : 'bg-bg-3 text-ink-2 hover:bg-bg-4'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>

      {/* Search + leave row */}
      <div className="flex items-center gap-2 mb-2">
        <div className="relative flex-1">
          <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-ink-4" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索成员"
            className="input pl-8 py-1.5 text-sm"
          />
        </div>
        {!iAmOwner && me && members?.some((m) => m.userId === me.id) && (
          <button
            onClick={selfLeave}
            className="btn-secondary text-xs text-accent-red"
            title="退出群组"
          >
            <DoorOpen size={13} /> 退出
          </button>
        )}
      </div>

      <div className="max-h-[60vh] overflow-y-auto -mx-5">
        {!members && (
          <div className="px-5 py-6 text-sm text-ink-4 text-center">加载中…</div>
        )}
        {members && members.length === 0 && (
          <div className="px-5 py-6 text-sm text-ink-4 text-center">还没有成员</div>
        )}
        {visibleMembers && visibleMembers.length === 0 && members && members.length > 0 && (
          <div className="px-5 py-6 text-sm text-ink-4 text-center">没有匹配的成员</div>
        )}
        {visibleMembers?.map((m) => {
          const r = roleLabel(m.role);
          const isMe = m.userId === me?.id;
          // What actions can the current user take on m?
          const canPromote = iAmOwner && m.userId !== ownerId && m.role === 0;
          const canDemote = iAmOwner && m.userId !== ownerId && m.role === 1;
          const canKickMe = !isMe && m.userId !== ownerId && (
            (iAmOwner) || (myRole === 1 && m.role === 0)
          );
          const hasActions = canPromote || canDemote || canKickMe;
          return (
            <div
              key={m.userId}
              className="px-5 py-2.5 flex items-center gap-3 hover:bg-bg-3/50 relative"
            >
              <Avatar name={m.nickname || m.username} size={36} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-ink-1 truncate">{m.nickname || m.username}</span>
                  {isMe && <span className="text-[10px] text-ink-4">（我）</span>}
                </div>
                <div className="text-xs text-ink-4 truncate">@{m.username}</div>
              </div>
              <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] ${r.color}`}>
                {r.icon}
                {r.text}
              </span>
              {hasActions && (
                <div className="relative">
                  <button
                    onClick={() => setOpenMenuFor(openMenuFor === m.userId ? null : m.userId)}
                    className="btn-icon w-7 h-7"
                    aria-label="更多"
                  >
                    <MoreVertical size={14} />
                  </button>
                  {openMenuFor === m.userId && (
                    <div className="absolute right-0 top-full mt-1 z-10 bg-bg-3 border border-bg-5/40 rounded-lg shadow-pop min-w-[140px] overflow-hidden">
                      {canPromote && (
                        <button
                          onClick={() => changeRole(m, 1)}
                          className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                        >
                          <UserPlus2 size={14} /> 升为管理员
                        </button>
                      )}
                      {canDemote && (
                        <button
                          onClick={() => changeRole(m, 0)}
                          className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-bg-4"
                        >
                          <Shield size={14} /> 降为成员
                        </button>
                      )}
                      {canKickMe && (
                        <button
                          onClick={() => kick(m)}
                          className="flex items-center gap-2 w-full px-3 py-2 text-sm text-left text-accent-red hover:bg-bg-4"
                        >
                          <UserMinus size={14} /> 踢出群组
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </Modal>
  );
}
