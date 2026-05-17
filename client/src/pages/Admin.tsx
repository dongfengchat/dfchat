import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  Ban,
  CheckCircle2,
  CircleOff,
  FileText,
  Gift,
  Hash,
  LogOut,
  MessageSquare,
  RadioTower,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
  Unlock,
  UserCircle,
  Users,
  X,
  XCircle,
} from 'lucide-react';
import {
  adminAccountPoolStats,
  adminBanLiveRoom,
  adminDeleteLiveRoom,
  adminForceEndLive,
  adminForceLogoutUser,
  adminGrantPremiumNumber,
  adminListLiveRooms,
  adminListPremiumNumbers,
  adminListUsers,
  adminReleasePremiumNumber,
  adminSetUserStatus,
  adminStats,
  adminUserLogins,
  type AdminLiveRoom,
  type AdminPremiumNumber,
  type AdminSegmentStat,
  type AdminStats,
  type AdminUser,
  type LoginLogEntry,
} from '@/api/client';
import { useUserStore } from '@/store/userStore';
import Avatar from '@/components/ui/Avatar';
import { toast } from '@/components/ui/Toast';
import Skeleton from '@/components/ui/Skeleton';
import TitleBar from '@/components/TitleBar';

function statusBadge(n: number) {
  if (n === 0) return <span className="text-accent-green text-xs font-medium">正常</span>;
  if (n === 1) return <span className="text-accent-amber text-xs font-medium">已禁用</span>;
  return <span className="text-accent-red text-xs font-medium">已删除</span>;
}

type AdminTab = 'users' | 'live' | 'pool';

export default function Admin() {
  const navigate = useNavigate();
  const me = useUserStore((s) => s.user);
  const [stats, setStats] = useState<AdminStats | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [search, setSearch] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState<AdminTab>('users');
  // Selected user opens the details modal (login history + force-logout
  // + ban). Click outside / X / Escape closes it.
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);

  useEffect(() => {
    if (!me) return;
    if (!me.isAdmin) {
      navigate('/home', { replace: true });
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const [s, u] = await Promise.all([adminStats(), adminListUsers({ limit: 100 })]);
        if (cancelled) return;
        setStats(s);
        setUsers(u);
      } catch (err: any) {
        if (!cancelled) setError(err.message ?? '加载失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [me, navigate]);

  async function refreshUsers(q?: string) {
    try {
      setUsers(await adminListUsers({ search: q, limit: 100 }));
    } catch (err: any) {
      setError(err.message ?? '加载失败');
    }
  }

  async function setStatus(u: AdminUser, status: number) {
    try {
      await adminSetUserStatus(u.id, status);
      await refreshUsers(search || undefined);
      toast(status === 0 ? '已启用账号' : '已禁用账号', 'success');
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }

  return (
    // h-screen + flex column so the title bar stays pinned and only the
    // inner content scrolls. Previous min-h-screen let the whole page
    // grow with content, scrolling the TitleBar (and the macOS traffic
    // lights / window-drag region with it) off-screen.
    <div className="h-screen flex flex-col bg-bg-1 text-ink-1">
      <TitleBar title="东风快信 · 管理后台" />
      <header className="h-14 px-6 border-b border-bg-5/40 bg-bg-2/60 backdrop-blur flex items-center gap-3 shrink-0">
        <button onClick={() => navigate('/home')} className="btn-icon" title="返回">
          <ArrowLeft size={18} />
        </button>
        <ShieldCheck size={18} className="text-accent-amber" />
        <h1 className="text-base font-semibold">管理后台</h1>
        <div className="text-xs text-ink-3 ml-2">@{me?.username}</div>
      </header>

      <div className="flex-1 min-h-0 overflow-y-auto">
      {error ? (
        <div className="p-6 text-accent-red">{error}</div>
      ) : (
        <>
          <section className="p-6">
            <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
              <StatCard label="用户总数" value={stats?.totalUsers} icon={<Users size={18} />} />
              <StatCard label="群组总数" value={stats?.totalGroups} icon={<UserCircle size={18} />} accent="text-brand-300" />
              <StatCard label="今日消息" value={stats?.messagesToday} icon={<MessageSquare size={18} />} accent="text-accent-green" />
              <StatCard label="消息总数" value={stats?.totalMessages} icon={<MessageSquare size={18} />} />
              <StatCard label="文件总数" value={stats?.totalFiles} icon={<FileText size={18} />} accent="text-accent-amber" />
            </div>
          </section>

          {/* Tab pills */}
          <div className="px-6">
            <div className="flex gap-1.5 border-b border-bg-5/40">
              <button
                onClick={() => setTab('users')}
                className={`px-4 py-2 text-sm rounded-t -mb-px transition-colors ${
                  tab === 'users' ? 'bg-bg-3 text-ink-1 border-b-2 border-brand-500' : 'text-ink-3 hover:text-ink-1'
                }`}
              >
                <Users size={14} className="inline mr-1.5 -mt-0.5" /> 用户管理
              </button>
              <button
                onClick={() => setTab('live')}
                className={`px-4 py-2 text-sm rounded-t -mb-px transition-colors ${
                  tab === 'live' ? 'bg-bg-3 text-ink-1 border-b-2 border-brand-500' : 'text-ink-3 hover:text-ink-1'
                }`}
              >
                <RadioTower size={14} className="inline mr-1.5 -mt-0.5" /> 直播管理
              </button>
              <button
                onClick={() => setTab('pool')}
                className={`px-4 py-2 text-sm rounded-t -mb-px transition-colors ${
                  tab === 'pool' ? 'bg-bg-3 text-ink-1 border-b-2 border-brand-500' : 'text-ink-3 hover:text-ink-1'
                }`}
              >
                <Hash size={14} className="inline mr-1.5 -mt-0.5" /> 号码池
              </button>
            </div>
          </div>

          {tab === 'live' && <LiveAdminPanel />}
          {tab === 'pool' && <PoolPanel />}

          {tab === 'users' && <section className="px-6 pb-6 pt-4">
            <div className="flex items-center gap-3 mb-3">
              <h2 className="text-base font-semibold">用户管理</h2>
              <div className="ml-auto relative w-72">
                <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-4" />
                <input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && refreshUsers(search || undefined)}
                  placeholder="搜索 账号/用户名/邮箱/昵称（回车）"
                  className="input pl-9 py-2 text-sm"
                />
              </div>
            </div>

            <div className="card overflow-x-auto">
              <table className="w-full text-sm min-w-[1100px]">
                <thead className="bg-bg-3 text-ink-3">
                  <tr>
                    <th className="text-left px-3 py-2.5 font-medium">账号</th>
                    <th className="text-left px-3 py-2.5 font-medium">用户</th>
                    <th className="text-left px-3 py-2.5 font-medium">邮箱</th>
                    <th className="text-left px-3 py-2.5 font-medium">注册 IP</th>
                    <th className="text-left px-3 py-2.5 font-medium">最近登录 IP</th>
                    <th className="text-left px-3 py-2.5 font-medium">状态</th>
                    <th className="text-left px-3 py-2.5 font-medium">注册时间</th>
                    <th className="text-right px-3 py-2.5 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {loading
                    ? Array.from({ length: 4 }).map((_, i) => (
                        <tr key={i} className="border-t border-bg-5/30">
                          <td className="px-3 py-3"><Skeleton className="h-4 w-16" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-32" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-40" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-24" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-24" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-12" /></td>
                          <td className="px-3 py-3"><Skeleton className="h-4 w-28" /></td>
                          <td className="px-3 py-3 text-right"><Skeleton className="h-8 w-16 inline-block" /></td>
                        </tr>
                      ))
                    : users.map((u) => (
                        <tr
                          key={u.id}
                          className="border-t border-bg-5/30 hover:bg-bg-3/50 cursor-pointer"
                          onClick={() => setSelectedUser(u)}
                          title="点击查看详情"
                        >
                          <td className="px-3 py-2.5">
                            <span className="font-mono text-sm text-ink-1">{u.accountNo}</span>
                            {u.isAdmin && (
                              <span className="ml-1.5 inline-flex items-center text-accent-amber" title="管理员">
                                <ShieldCheck size={11} />
                              </span>
                            )}
                          </td>
                          <td className="px-3 py-2.5">
                            <div className="flex items-center gap-2 min-w-0">
                              <Avatar name={u.nickname || u.username} size={28} />
                              <div className="min-w-0">
                                <div className="font-medium text-ink-1 truncate text-xs">{u.nickname}</div>
                                <div className="text-[11px] text-ink-3 truncate">@{u.username}</div>
                              </div>
                            </div>
                          </td>
                          <td className="px-3 py-2.5 text-ink-3 text-xs">
                            <div className="flex items-center gap-1.5">
                              <span className="truncate max-w-[180px]" title={u.email}>{u.email}</span>
                              {u.emailVerified ? (
                                <CheckCircle2 size={12} className="text-accent-green shrink-0" />
                              ) : (
                                <CircleOff size={12} className="text-ink-4 shrink-0" />
                              )}
                            </div>
                          </td>
                          <td className="px-3 py-2.5 text-ink-3 text-xs font-mono">{u.registeredFromIp || '—'}</td>
                          <td className="px-3 py-2.5 text-ink-3 text-xs font-mono">{u.lastLoginIp || '—'}</td>
                          <td className="px-3 py-2.5">{statusBadge(u.status)}</td>
                          <td className="px-3 py-2.5 text-ink-3 text-xs">
                            {new Date(u.createdAt).toLocaleString('zh-CN', { hour12: false })}
                          </td>
                          <td className="px-3 py-2.5 text-right" onClick={(e) => e.stopPropagation()}>
                            {u.status === 0 ? (
                              <button
                                onClick={() => setStatus(u, 1)}
                                className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-accent-amber/15 text-accent-amber hover:bg-accent-amber/25 text-xs"
                              >
                                <Ban size={12} /> 封号
                              </button>
                            ) : (
                              <button
                                onClick={() => setStatus(u, 0)}
                                className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-accent-green/15 text-accent-green hover:bg-accent-green/25 text-xs"
                              >
                                <CheckCircle2 size={12} /> 解封
                              </button>
                            )}
                          </td>
                        </tr>
                      ))}
                </tbody>
              </table>
            </div>
          </section>}
        </>
      )}
      </div>

      {/* Modal sits outside the scrollable inner area — it's
          position:fixed already, but keeping it as a sibling of the
          scroll container avoids any stacking-context surprises. */}
      {selectedUser && (
        <UserDetailsModal
          user={selectedUser}
          onClose={() => setSelectedUser(null)}
          onMutated={async () => {
            // Whatever the modal did (ban, force-logout) — reload the
            // table so the row reflects the new state.
            await refreshUsers(search || undefined);
            setSelectedUser(null);
          }}
        />
      )}
    </div>
  );
}

// UserDetailsModal — opened by clicking a row in the user table. Shows
// full profile + last 50 login attempts + ban / force-logout actions.
// Closed with X, click-outside, or Escape.
function UserDetailsModal({
  user,
  onClose,
  onMutated,
}: {
  user: AdminUser;
  onClose: () => void;
  onMutated: () => void;
}) {
  const [logs, setLogs] = useState<LoginLogEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const l = await adminUserLogins(user.id, 50);
        if (!cancelled) setLogs(l);
      } catch (e: any) {
        if (!cancelled) setErr(e?.message ?? '加载失败');
      }
    })();
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => { cancelled = true; document.removeEventListener('keydown', onKey); };
  }, [user.id, onClose]);

  async function forceLogout() {
    if (busy) return;
    setBusy(true);
    try {
      const r = await adminForceLogoutUser(user.id);
      toast(`已强制下线 ${r.revoked} 个会话`, 'success');
      onMutated();
    } catch (e: any) {
      toast(e?.message ?? '操作失败', 'error');
    } finally {
      setBusy(false);
    }
  }
  async function toggleBan() {
    if (busy) return;
    setBusy(true);
    try {
      await adminSetUserStatus(user.id, user.status === 0 ? 1 : 0);
      toast(user.status === 0 ? '账号已封禁 + 强制下线' : '账号已解封', 'success');
      onMutated();
    } catch (e: any) {
      toast(e?.message ?? '操作失败', 'error');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4 anim-fade"
      onClick={onClose}
    >
      <div
        className="card max-w-2xl w-full max-h-[85vh] overflow-hidden flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-5 py-4 border-b border-bg-5/40 flex items-center justify-between">
          <div className="flex items-center gap-3 min-w-0">
            <Avatar name={user.nickname || user.username} size={36} />
            <div className="min-w-0">
              <div className="text-base font-semibold truncate">{user.nickname || user.username}</div>
              <div className="text-xs text-ink-3">@{user.username} · 账号 <span className="font-mono">{user.accountNo}</span></div>
            </div>
          </div>
          <button onClick={onClose} className="btn-icon w-8 h-8"><X size={16} /></button>
        </div>

        <div className="px-5 py-4 space-y-4 overflow-y-auto">
          {/* Profile facts */}
          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <div className="flex justify-between"><dt className="text-ink-3">状态</dt><dd>{statusBadge(user.status)}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-3">角色</dt><dd>{user.isAdmin ? <span className="text-accent-amber inline-flex items-center gap-1"><ShieldCheck size={12} /> 管理员</span> : <span className="text-ink-4">普通</span>}</dd></div>
            <div className="flex justify-between col-span-2"><dt className="text-ink-3">邮箱</dt>
              <dd className="flex items-center gap-1 truncate"><span className="truncate" title={user.email}>{user.email}</span>
                {user.emailVerified ? <CheckCircle2 size={12} className="text-accent-green" /> : <CircleOff size={12} className="text-ink-4" />}
              </dd>
            </div>
            <div className="flex justify-between"><dt className="text-ink-3">注册 IP</dt><dd className="font-mono text-xs">{user.registeredFromIp || '—'}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-3">最近登录 IP</dt><dd className="font-mono text-xs">{user.lastLoginIp || '—'}</dd></div>
            <div className="flex justify-between col-span-2"><dt className="text-ink-3">注册时间</dt>
              <dd className="text-xs">{new Date(user.createdAt).toLocaleString('zh-CN', { hour12: false })}</dd>
            </div>
          </dl>

          {/* Actions */}
          <div className="flex gap-2 border-y border-bg-5/40 py-3">
            <button
              onClick={forceLogout}
              disabled={busy}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-bg-3 hover:bg-bg-4 text-sm"
              title="撤销该用户所有 refresh token，现有登录会话立即失效"
            >
              <LogOut size={13} /> 强制下线
            </button>
            <button
              onClick={toggleBan}
              disabled={busy}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded text-sm ${
                user.status === 0
                  ? 'bg-accent-amber/15 text-accent-amber hover:bg-accent-amber/25'
                  : 'bg-accent-green/15 text-accent-green hover:bg-accent-green/25'
              }`}
            >
              {user.status === 0 ? <><Ban size={13} /> 封号</> : <><CheckCircle2 size={13} /> 解封</>}
            </button>
          </div>

          {/* Login history */}
          <div>
            <h3 className="text-sm font-semibold flex items-center gap-1.5 mb-2">
              <Activity size={14} className="text-brand-300" /> 登录历史 <span className="text-xs text-ink-4 font-normal">最近 50 条</span>
            </h3>
            {err ? (
              <div className="text-xs text-accent-red">{err}</div>
            ) : !logs ? (
              <div className="text-xs text-ink-4">加载中…</div>
            ) : logs.length === 0 ? (
              <div className="text-xs text-ink-4">还没有登录记录</div>
            ) : (
              <ul className="space-y-1 max-h-72 overflow-y-auto">
                {logs.map((l) => (
                  <li
                    key={l.id}
                    className={`flex items-center gap-2 text-xs py-1.5 px-2 rounded ${
                      l.success ? 'bg-bg-2' : 'bg-accent-red/10 border border-accent-red/30'
                    }`}
                  >
                    {l.success
                      ? <CheckCircle2 size={11} className="text-accent-green shrink-0" />
                      : <XCircle size={11} className="text-accent-red shrink-0" />}
                    <span className="font-mono text-ink-1 shrink-0 w-32">{l.ip || '—'}</span>
                    <span className="text-ink-3 shrink-0 w-44">
                      {new Date(l.createdAt).toLocaleString('zh-CN', { hour12: false })}
                    </span>
                    <span className="text-ink-4 truncate" title={l.userAgent}>{l.userAgent}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function LiveAdminPanel() {
  const [rooms, setRooms] = useState<AdminLiveRoom[] | null>(null);
  const [filter, setFilter] = useState<'' | 'live' | 'ended' | 'banned'>('');
  const [busy, setBusy] = useState<string | null>(null);

  async function refresh() {
    try {
      setRooms(await adminListLiveRooms(filter || undefined));
    } catch (e: any) {
      toast(e.message ?? '加载失败', 'error');
    }
  }
  useEffect(() => { refresh(); /* eslint-disable-next-line */ }, [filter]);

  async function act(id: string, fn: () => Promise<void>, ok: string) {
    setBusy(id);
    try {
      await fn();
      toast(ok, 'success');
      await refresh();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setBusy(null);
    }
  }

  function statusPill(s: number) {
    if (s === 1) return <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-accent-red/20 text-accent-red"><span className="w-1.5 h-1.5 rounded-full bg-accent-red animate-pulse" /> 直播中</span>;
    if (s === 2) return <span className="px-2 py-0.5 rounded text-[11px] bg-bg-3 text-ink-3">已结束</span>;
    if (s === 3) return <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-accent-amber/20 text-accent-amber"><Ban size={10} /> 已封禁</span>;
    return <span className="px-2 py-0.5 rounded text-[11px] bg-bg-3 text-ink-3">空闲</span>;
  }

  return (
    <section className="px-6 pb-6 pt-4">
      <div className="flex items-center gap-3 mb-3">
        <h2 className="text-base font-semibold">直播间管理</h2>
        <div className="ml-auto flex gap-1.5 text-xs">
          {([
            { v: '', label: '全部' },
            { v: 'live', label: '直播中' },
            { v: 'ended', label: '已结束' },
            { v: 'banned', label: '已封禁' },
          ] as const).map((opt) => (
            <button
              key={opt.v}
              onClick={() => setFilter(opt.v as typeof filter)}
              className={`px-3 py-1 rounded-full transition-colors ${
                filter === opt.v ? 'bg-brand-500 text-white' : 'bg-bg-3 text-ink-2 hover:bg-bg-4'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      <div className="card overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-bg-3 text-ink-3">
            <tr>
              <th className="text-left px-4 py-2.5 font-medium">直播间</th>
              <th className="text-left px-4 py-2.5 font-medium">主播</th>
              <th className="text-left px-4 py-2.5 font-medium">状态</th>
              <th className="text-left px-4 py-2.5 font-medium">在线/累计</th>
              <th className="text-left px-4 py-2.5 font-medium">开始</th>
              <th className="text-right px-4 py-2.5 font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {!rooms && (
              <tr><td colSpan={6} className="text-center py-8 text-ink-4">加载中…</td></tr>
            )}
            {rooms && rooms.length === 0 && (
              <tr><td colSpan={6} className="text-center py-8 text-ink-4">没有直播间</td></tr>
            )}
            {rooms?.map((r) => {
              const isBusy = busy === r.id;
              const live = r.status === 1;
              const banned = r.status === 3;
              return (
                <tr key={r.id} className="border-t border-bg-5/30 hover:bg-bg-3/50">
                  <td className="px-4 py-2.5">
                    <div className="font-medium text-ink-1 truncate max-w-xs">{r.title}</div>
                    <div className="text-xs text-ink-3">
                      #{r.id} {r.category && <>· {r.category}</>} {r.isTest && <span className="text-brand-300">· 试播</span>}
                    </div>
                  </td>
                  <td className="px-4 py-2.5 text-xs text-ink-3">@{r.ownerName} · #{r.ownerId}</td>
                  <td className="px-4 py-2.5">{statusPill(r.status)}</td>
                  <td className="px-4 py-2.5 text-xs text-ink-3">{r.viewerCount} / {r.totalViews}</td>
                  <td className="px-4 py-2.5 text-xs text-ink-3">
                    {r.startedAt ? new Date(r.startedAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—'}
                  </td>
                  <td className="px-4 py-2.5 text-right space-x-1">
                    {live && (
                      <button
                        disabled={isBusy}
                        onClick={() => {
                          if (!confirm(`强制下播「${r.title}」？\n推流密钥会被轮换，OBS 无法重连。`)) return;
                          act(r.id, () => adminForceEndLive(r.id), '已强制下播');
                        }}
                        className="inline-flex items-center gap-1 px-2 py-1 rounded bg-accent-red/15 text-accent-red hover:bg-accent-red/25 text-xs"
                      >
                        <CircleOff size={11} /> 强制下播
                      </button>
                    )}
                    {!banned ? (
                      <button
                        disabled={isBusy}
                        onClick={() => {
                          const reason = prompt('封禁原因（写入审计日志）：', '违规内容') ?? '';
                          act(r.id, () => adminBanLiveRoom(r.id, true, reason), '已封禁');
                        }}
                        className="inline-flex items-center gap-1 px-2 py-1 rounded bg-accent-amber/15 text-accent-amber hover:bg-accent-amber/25 text-xs"
                      >
                        <Ban size={11} /> 封禁
                      </button>
                    ) : (
                      <button
                        disabled={isBusy}
                        onClick={() => act(r.id, () => adminBanLiveRoom(r.id, false), '已解封')}
                        className="inline-flex items-center gap-1 px-2 py-1 rounded bg-accent-green/15 text-accent-green hover:bg-accent-green/25 text-xs"
                      >
                        <CheckCircle2 size={11} /> 解封
                      </button>
                    )}
                    <button
                      disabled={isBusy}
                      onClick={() => {
                        if (!confirm(`彻底删除「${r.title}」？所有数据（弹幕、关注、记录）一并删除。`)) return;
                        act(r.id, () => adminDeleteLiveRoom(r.id), '已删除');
                      }}
                      className="inline-flex items-center gap-1 px-2 py-1 rounded bg-bg-3 text-ink-3 hover:bg-bg-4 text-xs"
                    >
                      <Trash2 size={11} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function StatCard({
  label,
  value,
  icon,
  accent = 'text-ink-1',
}: {
  label: string;
  value?: number;
  icon: React.ReactNode;
  accent?: string;
}) {
  return (
    <div className="card p-4">
      <div className="flex items-center justify-between">
        <div className="text-xs text-ink-3">{label}</div>
        <div className={`${accent}`}>{icon}</div>
      </div>
      <div className="text-2xl font-semibold mt-2">
        {value == null ? <Skeleton className="h-7 w-16" /> : value.toLocaleString()}
      </div>
    </div>
  );
}

// PoolPanel shows every account-number segment with a fill bar:
// claimed (occupied) + reserved (in-flight draws) + locked (premium,
// admin-only) + free. Lets admins see when the next segment is about
// to open and spot pool-drain anomalies (e.g. reserved >> normal
// pattern = bot draws sitting on stock).
function PoolPanel() {
  const [segments, setSegments] = useState<AdminSegmentStat[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const s = await adminAccountPoolStats();
        if (!cancelled) setSegments(s);
      } catch (e: any) {
        if (!cancelled) setError(e?.message ?? '加载失败');
      }
    };
    void load();
    // Auto-refresh every 30s so pool-drain shows up live.
    const t = setInterval(() => void load(), 30000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  if (error) return <div className="p-6 text-accent-red">{error}</div>;

  return (
    <section className="px-6 pb-6 pt-4 space-y-4">
      <div className="flex items-center gap-3">
        <h2 className="text-base font-semibold">账号号码池</h2>
        <span className="text-xs text-ink-4">每 30s 自动刷新</span>
      </div>

      {!segments ? (
        <div className="card p-6"><Skeleton className="h-32 w-full" /></div>
      ) : segments.length === 0 ? (
        <div className="card p-6 text-ink-3 text-sm">还没有开启任何号段</div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {segments.map((s) => {
            const pctClaimed = s.total > 0 ? (s.claimed / s.total) * 100 : 0;
            const pctReserved = s.total > 0 ? (s.reserved / s.total) * 100 : 0;
            const pctLocked = s.total > 0 ? (s.locked / s.total) * 100 : 0;
            // Warn when free dips low — next-segment trigger is at 200 free.
            const lowFree = s.free < 500;
            return (
              <div key={s.segmentNo} className="card p-5 space-y-3">
                <div className="flex items-start justify-between">
                  <div>
                    <div className="text-xs text-ink-3">段 {s.segmentNo} · {s.state}</div>
                    <div className="font-mono text-base">
                      {s.rangeStart.toLocaleString()} – {s.rangeEnd.toLocaleString()}
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="text-xs text-ink-4">开放于</div>
                    <div className="text-xs text-ink-3">{new Date(s.openedAt).toLocaleDateString()}</div>
                  </div>
                </div>

                {/* Composite fill bar */}
                <div className="h-3 rounded-full overflow-hidden bg-bg-3 flex">
                  <div className="bg-accent-green" style={{ width: `${pctClaimed}%` }} title={`已注册 ${s.claimed}`} />
                  <div className="bg-accent-amber" style={{ width: `${pctReserved}%` }} title={`在摇 ${s.reserved}`} />
                  <div className="bg-brand-500" style={{ width: `${pctLocked}%` }} title={`靓号锁定 ${s.locked}`} />
                </div>

                <div className="grid grid-cols-4 gap-2 text-xs">
                  <PoolStat label="已注册" value={s.claimed} color="text-accent-green" />
                  <PoolStat label="在摇" value={s.reserved} color="text-accent-amber" />
                  <PoolStat label="靓号" value={s.locked} color="text-brand-300" />
                  <PoolStat label="可用" value={s.free} color={lowFree ? 'text-accent-red' : 'text-ink-1'} />
                </div>
                {lowFree && (
                  <div className="text-[11px] text-accent-red">
                    可用号 &lt; 500，下次摇号会自动开下一段
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <PremiumNumbersPanel />
    </section>
  );
}

// PremiumNumbersPanel lists the locked premium numbers and lets admins
// either grant one to a specific user (by their current account_no) or
// release the number back to the random draw pool.
function PremiumNumbersPanel() {
  const [nums, setNums] = useState<AdminPremiumNumber[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Track which row is mid-grant so we can show an inline input + spinner.
  const [grantingNo, setGrantingNo] = useState<string | null>(null);
  const [grantInput, setGrantInput] = useState('');
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      setErr(null);
      setNums(await adminListPremiumNumbers());
    } catch (e: any) {
      setErr(e?.message ?? '加载失败');
    }
  }
  useEffect(() => { void load(); }, []);

  async function grant(premiumNo: string) {
    if (!grantInput.trim()) {
      toast('请输入接收账号', 'warn');
      return;
    }
    setBusy(true);
    try {
      const r = await adminGrantPremiumNumber(premiumNo, grantInput.trim());
      toast(`已将 ${r.newAccountNo} 赠送给账号（原号 ${r.previousAccountNo}）`, 'success');
      setGrantingNo(null);
      setGrantInput('');
      await load();
    } catch (e: any) {
      toast(e?.message ?? '赠送失败', 'error');
    } finally {
      setBusy(false);
    }
  }
  async function release(premiumNo: string) {
    if (!confirm(`确定释放 ${premiumNo} 到随机摇号池？此后任何人都可能抽到。`)) return;
    setBusy(true);
    try {
      await adminReleasePremiumNumber(premiumNo);
      toast(`${premiumNo} 已释放回摇号池`, 'success');
      await load();
    } catch (e: any) {
      toast(e?.message ?? '释放失败', 'error');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card overflow-hidden">
      <div className="px-4 py-3 border-b border-bg-5/30 flex items-center gap-3">
        <Hash size={14} className="text-brand-300" />
        <h3 className="text-sm font-semibold">锁定的靓号</h3>
        <span className="text-xs text-ink-4 ml-auto">
          {nums == null ? '加载中…' : `${nums.length} 个`}
        </span>
        <button onClick={load} className="btn-icon w-7 h-7" title="刷新">
          <RefreshCw size={12} />
        </button>
      </div>

      {err && <div className="p-4 text-xs text-accent-red">{err}</div>}

      {nums && nums.length === 0 && (
        <div className="p-6 text-xs text-ink-3 text-center">
          当前没有锁定的靓号 — 段开放时全数字相同 / 顺号 / 回文等模式才会进入锁池
        </div>
      )}

      {nums && nums.length > 0 && (
        <div className="max-h-[480px] overflow-y-auto">
          <table className="w-full text-sm">
            <thead className="bg-bg-3 text-ink-3 sticky top-0">
              <tr>
                <th className="text-left px-4 py-2 font-medium">号码</th>
                <th className="text-left px-4 py-2 font-medium">段</th>
                <th className="text-left px-4 py-2 font-medium">归属</th>
                <th className="text-right px-4 py-2 font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {nums.map((n) => (
                <tr key={n.accountNo} className="border-t border-bg-5/30">
                  <td className="px-4 py-2 font-mono">{n.accountNo}</td>
                  <td className="px-4 py-2 text-ink-3 text-xs">段 {n.segmentNo}</td>
                  <td className="px-4 py-2 text-xs">
                    {n.claimed ? (
                      <span className="text-accent-green">@{n.ownerName}</span>
                    ) : (
                      <span className="text-ink-4">空闲（锁定中）</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {n.claimed ? (
                      <span className="text-xs text-ink-4">已被使用</span>
                    ) : grantingNo === n.accountNo ? (
                      <div className="inline-flex items-center gap-1.5">
                        <input
                          type="text"
                          value={grantInput}
                          onChange={(e) => setGrantInput(e.target.value)}
                          placeholder="接收账号"
                          className="input py-1 text-xs w-32 font-mono"
                          autoFocus
                          disabled={busy}
                        />
                        <button
                          onClick={() => grant(n.accountNo)}
                          disabled={busy}
                          className="text-xs px-2 py-1 rounded bg-accent-green/15 text-accent-green hover:bg-accent-green/25"
                        >
                          确认
                        </button>
                        <button
                          onClick={() => { setGrantingNo(null); setGrantInput(''); }}
                          disabled={busy}
                          className="text-xs px-2 py-1 rounded text-ink-3 hover:bg-bg-3"
                        >
                          取消
                        </button>
                      </div>
                    ) : (
                      <div className="inline-flex gap-1.5">
                        <button
                          onClick={() => { setGrantingNo(n.accountNo); setGrantInput(''); }}
                          className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded bg-brand-500/15 text-brand-300 hover:bg-brand-500/25"
                        >
                          <Gift size={11} /> 赠送
                        </button>
                        <button
                          onClick={() => release(n.accountNo)}
                          className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded bg-bg-3 text-ink-3 hover:bg-bg-4"
                          title="放回随机摇号池"
                        >
                          <Unlock size={11} /> 释放
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function PoolStat({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-ink-4">{label}</span>
      <span className={`font-mono font-semibold ${color}`}>{value.toLocaleString()}</span>
    </div>
  );
}
