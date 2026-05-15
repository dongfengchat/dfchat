import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  Ban,
  CheckCircle2,
  CircleOff,
  FileText,
  MessageSquare,
  RadioTower,
  Search,
  ShieldCheck,
  Trash2,
  UserCircle,
  Users,
  XCircle,
} from 'lucide-react';
import {
  adminBanLiveRoom,
  adminDeleteLiveRoom,
  adminForceEndLive,
  adminListLiveRooms,
  adminListUsers,
  adminSetUserStatus,
  adminStats,
  type AdminLiveRoom,
  type AdminStats,
  type AdminUser,
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

type AdminTab = 'users' | 'live';

export default function Admin() {
  const navigate = useNavigate();
  const me = useUserStore((s) => s.user);
  const [stats, setStats] = useState<AdminStats | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [search, setSearch] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState<AdminTab>('users');

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
    <div className="min-h-screen bg-bg-1 text-ink-1">
      <TitleBar title="东风快信 · 管理后台" />
      <header className="h-14 px-6 border-b border-bg-5/40 bg-bg-2/60 backdrop-blur flex items-center gap-3">
        <button onClick={() => navigate('/home')} className="btn-icon" title="返回">
          <ArrowLeft size={18} />
        </button>
        <ShieldCheck size={18} className="text-accent-amber" />
        <h1 className="text-base font-semibold">管理后台</h1>
        <div className="text-xs text-ink-3 ml-2">@{me?.username}</div>
      </header>

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
            </div>
          </div>

          {tab === 'live' && <LiveAdminPanel />}

          {tab === 'users' && <section className="px-6 pb-6 pt-4">
            <div className="flex items-center gap-3 mb-3">
              <h2 className="text-base font-semibold">用户管理</h2>
              <div className="ml-auto relative w-72">
                <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-4" />
                <input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && refreshUsers(search || undefined)}
                  placeholder="搜索 用户名/邮箱/昵称（回车）"
                  className="input pl-9 py-2 text-sm"
                />
              </div>
            </div>

            <div className="card overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-bg-3 text-ink-3">
                  <tr>
                    <th className="text-left px-4 py-2.5 font-medium">用户</th>
                    <th className="text-left px-4 py-2.5 font-medium">邮箱</th>
                    <th className="text-left px-4 py-2.5 font-medium">状态</th>
                    <th className="text-left px-4 py-2.5 font-medium">角色</th>
                    <th className="text-left px-4 py-2.5 font-medium">最近登录</th>
                    <th className="text-right px-4 py-2.5 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {loading
                    ? Array.from({ length: 4 }).map((_, i) => (
                        <tr key={i} className="border-t border-bg-5/30">
                          <td className="px-4 py-3"><Skeleton className="h-4 w-40" /></td>
                          <td className="px-4 py-3"><Skeleton className="h-4 w-48" /></td>
                          <td className="px-4 py-3"><Skeleton className="h-4 w-12" /></td>
                          <td className="px-4 py-3"><Skeleton className="h-4 w-12" /></td>
                          <td className="px-4 py-3"><Skeleton className="h-4 w-28" /></td>
                          <td className="px-4 py-3 text-right"><Skeleton className="h-8 w-16 inline-block" /></td>
                        </tr>
                      ))
                    : users.map((u) => (
                        <tr key={u.id} className="border-t border-bg-5/30 hover:bg-bg-3/50">
                          <td className="px-4 py-2.5">
                            <div className="flex items-center gap-3">
                              <Avatar name={u.nickname || u.username} size={32} />
                              <div className="min-w-0">
                                <div className="font-medium text-ink-1 truncate">{u.nickname}</div>
                                <div className="text-xs text-ink-3 truncate">@{u.username} · #{u.id}</div>
                              </div>
                            </div>
                          </td>
                          <td className="px-4 py-2.5 text-ink-3 text-xs">{u.email}</td>
                          <td className="px-4 py-2.5">{statusBadge(u.status)}</td>
                          <td className="px-4 py-2.5">
                            {u.isAdmin ? (
                              <span className="inline-flex items-center gap-1 text-accent-amber text-xs">
                                <ShieldCheck size={12} /> 管理员
                              </span>
                            ) : (
                              <span className="text-ink-4 text-xs">普通</span>
                            )}
                          </td>
                          <td className="px-4 py-2.5 text-ink-3 text-xs">
                            {u.lastLoginAt ? new Date(u.lastLoginAt).toLocaleString() : '—'}
                          </td>
                          <td className="px-4 py-2.5 text-right">
                            {u.status === 0 ? (
                              <button
                                onClick={() => setStatus(u, 1)}
                                className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-accent-amber/15 text-accent-amber hover:bg-accent-amber/25 text-xs"
                              >
                                <Ban size={12} /> 禁用
                              </button>
                            ) : (
                              <button
                                onClick={() => setStatus(u, 0)}
                                className="inline-flex items-center gap-1 px-2.5 py-1 rounded bg-accent-green/15 text-accent-green hover:bg-accent-green/25 text-xs"
                              >
                                <CheckCircle2 size={12} /> 启用
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
