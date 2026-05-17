import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Database,
  Download,
  Info,
  KeyRound,
  Loader2,
  LogOut,
  Mail,
  Monitor,
  Pencil,
  Save,
  ShieldCheck,
  Smartphone,
  Trash2,
  Upload,
  UserCircle,
  X,
  XCircle,
} from 'lucide-react';
import {
  changePassword,
  deleteMe,
  listSessions,
  logoutServer,
  recentLogins,
  requestEmailChange,
  revokeOtherSessions,
  revokeSession,
  sendVerificationEmail,
  updateMe,
  uploadBlob,
  type LoginLogEntry,
} from '@/api/client';
import { useSeqStore } from '@/sync/seqStore';
import { useUserStore } from '@/store/userStore';
import type { SessionItem } from '@/types';
import Avatar from '@/components/ui/Avatar';
import TitleBar from '@/components/TitleBar';
import { toast } from '@/components/ui/Toast';

type Tab = 'profile' | 'security' | 'devices' | 'archive' | 'about';

function relative(iso?: string): string {
  if (!iso) return '从未使用';
  const t = new Date(iso).getTime();
  const delta = Date.now() - t;
  const m = Math.floor(delta / 60000);
  if (m < 1) return '刚刚';
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  return new Date(t).toLocaleDateString();
}

export default function Settings() {
  const navigate = useNavigate();
  const me = useUserStore((s) => s.user);
  const setSession = useUserStore((s) => s.setSession);
  const accessToken = useUserStore((s) => s.accessToken);
  const [tab, setTab] = useState<Tab>('profile');

  if (!me) {
    navigate('/login', { replace: true });
    return null;
  }

  return (
    <div className="min-h-screen bg-bg-1 text-ink-1 flex flex-col">
      <TitleBar title="东风快信 · 设置" />
      <header className="h-14 px-6 border-b border-bg-5/40 bg-bg-2/60 backdrop-blur flex items-center gap-3">
        <button onClick={() => navigate('/home')} className="btn-icon" title="返回">
          <ArrowLeft size={18} />
        </button>
        <h1 className="text-base font-semibold">设置</h1>
      </header>

      <div className="flex-1 flex max-w-5xl w-full mx-auto p-6 gap-6">
        <nav className="w-48 shrink-0 space-y-1">
          <TabButton active={tab === 'profile'} onClick={() => setTab('profile')} icon={<UserCircle size={16} />} label="个人资料" />
          <TabButton active={tab === 'security'} onClick={() => setTab('security')} icon={<KeyRound size={16} />} label="账号安全" />
          <TabButton active={tab === 'devices'} onClick={() => setTab('devices')} icon={<Monitor size={16} />} label="登录设备" />
          <TabButton active={tab === 'archive'} onClick={() => setTab('archive')} icon={<Database size={16} />} label="本地归档" />
          <TabButton active={tab === 'about'} onClick={() => setTab('about')} icon={<Info size={16} />} label="关于" />
        </nav>

        <main className="flex-1 min-w-0">
          {tab === 'profile' && (
            <ProfileTab
              me={me}
              onSaved={(u) => accessToken && setSession(u, accessToken)}
            />
          )}
          {tab === 'security' && <SecurityTab />}
          {tab === 'devices' && <DevicesTab />}
          {tab === 'archive' && <ArchiveTab />}
          {tab === 'about' && <AboutTab />}
        </main>
      </div>
    </div>
  );
}

function TabButton({
  active,
  onClick,
  icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <button
      onClick={onClick}
      className={`w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm transition-colors ${
        active ? 'bg-brand-500/15 text-ink-1' : 'text-ink-3 hover:bg-bg-3 hover:text-ink-1'
      }`}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}

function ProfileTab({ me, onSaved }: { me: ReturnType<typeof useUserStore.getState>['user']; onSaved: (u: NonNullable<ReturnType<typeof useUserStore.getState>['user']>) => void }) {
  const [nickname, setNickname] = useState(me?.nickname ?? '');
  const [bio, setBio] = useState(me?.bio ?? '');
  const [avatarUrl, setAvatarUrl] = useState(me?.avatarUrl ?? '');
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  async function save() {
    setSaving(true);
    try {
      const updated = await updateMe({
        nickname: nickname !== me?.nickname ? nickname : undefined,
        bio: bio !== me?.bio ? bio : undefined,
        avatarUrl: avatarUrl !== me?.avatarUrl ? avatarUrl : undefined,
      });
      onSaved(updated);
      toast('资料已更新', 'success');
    } catch (err: any) {
      toast(err.message ?? '保存失败', 'error');
    } finally {
      setSaving(false);
    }
  }

  async function onPickAvatar(file: File) {
    if (!file.type.startsWith('image/')) {
      toast('请选择图片文件', 'warn');
      return;
    }
    setUploading(true);
    try {
      const uploaded = await uploadBlob(file, file.name, 'image');
      setAvatarUrl(uploaded.url);
      // Auto-save the avatar so the user doesn't have to click "保存" twice.
      const updated = await updateMe({ avatarUrl: uploaded.url });
      onSaved(updated);
      toast('头像已更新', 'success');
    } catch (err: any) {
      toast(err.message ?? '上传失败', 'error');
    } finally {
      setUploading(false);
    }
  }

  return (
    <section className="card p-6 space-y-5 anim-fade">
      <div>
        <h2 className="text-base font-semibold">个人资料</h2>
        <p className="text-xs text-ink-3 mt-1">其他人看到的就是这些信息</p>
      </div>

      <div className="flex items-center gap-4">
        <Avatar name={nickname || me?.username || '?'} src={avatarUrl || undefined} size={72} />
        <div className="flex flex-col gap-2">
          <button
            onClick={() => fileRef.current?.click()}
            disabled={uploading}
            className="btn-secondary"
          >
            {uploading ? <Loader2 size={14} className="animate-spin" /> : null}
            上传头像
          </button>
          <button
            onClick={() => setAvatarUrl('')}
            disabled={!avatarUrl || uploading}
            className="btn-ghost text-xs"
          >
            移除
          </button>
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void onPickAvatar(f);
              e.target.value = '';
            }}
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">用户名</label>
        <input className="input opacity-60" value={me?.username ?? ''} disabled />
        <div className="text-[11px] text-ink-4">用户名注册后无法修改</div>
      </div>

      <EmailRow email={me?.email ?? ''} verified={!!me?.emailVerified} onVerified={() => onSaved({ ...(me as NonNullable<typeof me>), emailVerified: true })} />

      {/* Read-only account metadata. accountNo is the public 6+ digit
          number (also accepted as a login identifier); the internal row
          id is never shown. */}
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <label className="text-xs text-ink-3">账号</label>
          <input
            className="input opacity-60 font-mono tracking-wider"
            value={me?.accountNo ?? ''}
            disabled
          />
          <div className="text-[11px] text-ink-4">登录时可以用这个账号 + 密码</div>
        </div>
        <div className="space-y-1.5">
          <label className="text-xs text-ink-3">注册时间</label>
          <input
            className="input opacity-60"
            value={me?.createdAt ? new Date(me.createdAt).toLocaleString('zh-CN', { hour12: false }) : ''}
            disabled
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">昵称</label>
        <input
          className="input"
          maxLength={64}
          value={nickname}
          onChange={(e) => setNickname(e.target.value)}
          placeholder="给别人看的名字"
        />
      </div>

      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">个性签名</label>
        <textarea
          className="input min-h-[80px] resize-none"
          maxLength={255}
          value={bio}
          onChange={(e) => setBio(e.target.value)}
          placeholder="说点什么…"
        />
        <div className="text-[11px] text-ink-4 text-right">{bio.length}/255</div>
      </div>

      <div>
        <button
          onClick={save}
          disabled={saving || (nickname === me?.nickname && bio === (me?.bio ?? '') && avatarUrl === (me?.avatarUrl ?? ''))}
          className="btn-primary"
        >
          {saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
          保存修改
        </button>
      </div>
    </section>
  );
}

// EmailRow shows the user's registered email + verification status. If
// unverified, lets the user trigger the verification mail right here.
// If verified, lets them change the email to a new address (which must
// be confirmed via a click-link on the new mailbox before the swap is
// applied — the user's account email won't change until then).
function EmailRow({ email, verified, onVerified }: { email: string; verified: boolean; onVerified: () => void }) {
  const [sending, setSending] = useState(false);
  const [sentAt, setSentAt] = useState<number | null>(null);
  const [now, setNow] = useState(Date.now());
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    if (!sentAt) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [sentAt]);

  const cooldownLeft = sentAt ? Math.max(0, 60 - Math.floor((now - sentAt) / 1000)) : 0;

  async function send() {
    setSending(true);
    try {
      const res = await sendVerificationEmail();
      if (res.alreadyVerified) {
        toast('邮箱已验证', 'success');
        onVerified();
        return;
      }
      setSentAt(Date.now());
      if (res.devLink) {
        toast('开发模式：链接已在后端日志', 'info');
      } else {
        toast(`验证邮件已发送至 ${email}，去收件箱查收（也看一下垃圾箱）`, 'success');
      }
    } catch (e: any) {
      toast(e.message ?? '发送失败', 'error');
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="space-y-1.5">
      <label className="text-xs text-ink-3">邮箱</label>
      {editing ? (
        <EmailChangeForm currentEmail={email} onCancel={() => setEditing(false)} onSubmitted={() => setEditing(false)} />
      ) : (
        <>
          <div className="flex items-center gap-2">
            <input className="input opacity-60 flex-1" value={email} disabled />
            {verified ? (
              <>
                <span className="text-xs text-accent-green flex items-center gap-1 shrink-0">
                  <CheckCircle2 size={14} /> 已验证
                </span>
                <button
                  onClick={() => setEditing(true)}
                  className="btn-secondary text-xs py-1 shrink-0"
                  title="修改邮箱（新邮箱需要点击确认链接才会生效）"
                >
                  <Pencil size={12} /> 修改
                </button>
              </>
            ) : (
              <button
                onClick={send}
                disabled={sending || cooldownLeft > 0}
                className="btn-secondary text-xs py-1 shrink-0"
                title="发送验证邮件到这个邮箱"
              >
                {sending ? <Loader2 size={12} className="animate-spin" /> : <Mail size={12} />}
                {sending ? '发送中…' : cooldownLeft > 0 ? `${cooldownLeft}s 后重发` : '发送验证邮件'}
              </button>
            )}
          </div>
          <div className="text-[11px] text-ink-4">
            {verified
              ? '忘记密码时会发送重置链接到这个邮箱'
              : '验证后才能在忘记密码时收到重置邮件 / 修改邮箱'}
          </div>
        </>
      )}
    </div>
  );
}

// EmailChangeForm asks for the new address + the current password
// (re-auth defence against open-session hijack). On success, we just
// tell the user to check the new mailbox — the actual email swap is
// gated on them clicking the confirmation link there. Their visible
// account email stays unchanged until then.
function EmailChangeForm({ currentEmail, onCancel, onSubmitted }: { currentEmail: string; onCancel: () => void; onSubmitted: () => void }) {
  const [newEmail, setNewEmail] = useState('');
  const [pw, setPw] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit() {
    setErr(null);
    const trimmed = newEmail.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)) {
      setErr('请输入合法的邮箱');
      return;
    }
    if (trimmed.toLowerCase() === currentEmail.toLowerCase()) {
      setErr('新邮箱不能和当前邮箱相同');
      return;
    }
    if (!pw) {
      setErr('请输入当前密码');
      return;
    }
    setSubmitting(true);
    try {
      const res = await requestEmailChange(trimmed, pw);
      if (res.devLink) {
        toast('开发模式：确认链接已在后端日志', 'info');
      } else {
        toast(`已发送确认邮件到 ${trimmed}。点击邮件里的链接后邮箱才会真正变更（1 小时内有效）。`, 'success');
      }
      onSubmitted();
    } catch (e: any) {
      setErr(e?.message ?? '请求失败');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="space-y-2 p-3 rounded-lg bg-bg-2 border border-bg-3">
      <div className="text-xs text-ink-3">
        新邮箱将收到一封确认邮件 · 点击链接后才生效，过期时间 1 小时
      </div>
      <input
        className="input"
        placeholder="新邮箱地址"
        type="email"
        value={newEmail}
        onChange={(e) => setNewEmail(e.target.value)}
        autoFocus
        disabled={submitting}
      />
      <input
        className="input"
        placeholder="当前密码（用于二次确认身份）"
        type="password"
        value={pw}
        onChange={(e) => setPw(e.target.value)}
        disabled={submitting}
      />
      {err ? <div className="text-xs text-accent-red">{err}</div> : null}
      <div className="flex gap-2">
        <button onClick={submit} disabled={submitting} className="btn-primary text-xs py-1.5">
          {submitting ? <Loader2 size={12} className="animate-spin" /> : <Mail size={12} />}
          {submitting ? '发送中…' : '发送确认邮件'}
        </button>
        <button onClick={onCancel} disabled={submitting} className="btn-ghost text-xs py-1.5">
          <X size={12} /> 取消
        </button>
      </div>
    </div>
  );
}

function SecurityTab() {
  const navigate = useNavigate();
  const { clear } = useUserStore.getState();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [deletePassword, setDeletePassword] = useState('');
  const [deleting, setDeleting] = useState(false);

  async function submit() {
    setErr(null);
    if (next.length < 8) {
      setErr('新密码至少 8 位');
      return;
    }
    if (next !== confirm) {
      setErr('两次输入的新密码不一致');
      return;
    }
    setSaving(true);
    try {
      await changePassword(current, next);
      toast('密码已更新', 'success');
      setCurrent(''); setNext(''); setConfirm('');
    } catch (e: any) {
      setErr(e.message ?? '修改失败');
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="card p-6 space-y-5 anim-fade max-w-xl">
      <div>
        <h2 className="text-base font-semibold">修改密码</h2>
        <p className="text-xs text-ink-3 mt-1">修改后其它设备需要重新登录。</p>
      </div>
      {err && (
        <div className="text-sm text-accent-red bg-accent-red/10 border border-accent-red/40 rounded-lg px-3 py-2">
          {err}
        </div>
      )}
      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">当前密码</label>
        <input className="input" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      </div>
      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">新密码 <span className="text-ink-4">（至少 8 位）</span></label>
        <input className="input" type="password" value={next} onChange={(e) => setNext(e.target.value)} />
      </div>
      <div className="space-y-1.5">
        <label className="text-xs text-ink-3">确认新密码</label>
        <input className="input" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </div>
      <button
        onClick={submit}
        disabled={saving || !current || !next || !confirm}
        className="btn-primary"
      >
        {saving ? <Loader2 size={14} className="animate-spin" /> : <KeyRound size={14} />}
        更新密码
      </button>

      <RecentLoginsCard />

      <div className="pt-6 mt-6 border-t border-bg-5/40 space-y-3">
        <div>
          <h3 className="text-sm font-semibold text-accent-red flex items-center gap-1.5">
            <AlertTriangle size={14} /> 危险区域
          </h3>
          <p className="text-xs text-ink-3 mt-1">
            注销账号后，你的消息历史会保留以便他人继续访问，但你的资料、邮箱与密码将立刻销毁，且无法登录。
          </p>
        </div>
        <input
          className="input"
          type="password"
          placeholder="输入当前密码以确认注销"
          value={deletePassword}
          onChange={(e) => setDeletePassword(e.target.value)}
        />
        <button
          onClick={async () => {
            if (!deletePassword) return;
            if (!window.confirm(
              '此操作不可撤销。\n注销后所有设备会立即下线，你的账号将无法登录。\n确认继续吗？',
            )) return;
            setDeleting(true);
            try {
              await deleteMe(deletePassword);
              toast('账号已注销', 'success');
              await logoutServer().catch(() => {});
              useSeqStore.getState().clearAll();
              clear();
              navigate('/login', { replace: true });
            } catch (e: any) {
              toast(e.message ?? '注销失败', 'error');
            } finally {
              setDeleting(false);
            }
          }}
          disabled={!deletePassword || deleting}
          className="btn-danger"
        >
          {deleting ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
          永久注销账号
        </button>
      </div>
    </section>
  );
}

// RecentLoginsCard surfaces the user's own login history so they can
// spot a hostile login ("I never logged in from 1.2.3.4 last Tuesday").
// Pulls the server's last 20 attempts. Failures are shown in red — a
// streak of failed-from-strange-IP followed by a success is exactly
// the phishing signal we want to expose.
function RecentLoginsCard() {
  const [logs, setLogs] = useState<LoginLogEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState(true);

  async function load() {
    try {
      setErr(null);
      setLogs(await recentLogins());
    } catch (e: any) {
      setErr(e.message ?? '加载失败');
    }
  }
  useEffect(() => { void load(); }, []);

  return (
    <div className="pt-6 mt-6 border-t border-bg-5/40 space-y-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold flex items-center gap-1.5">
            <Activity size={14} className="text-brand-300" /> 登录历史
          </h3>
          <p className="text-xs text-ink-3 mt-1">
            出现陌生 IP 或失败记录？立刻 <span className="text-brand-300">改密码</span> + <span className="text-brand-300">在「设备」里退出全部</span>
          </p>
        </div>
        <button onClick={load} className="btn-icon w-7 h-7" title="刷新">
          <Activity size={12} />
        </button>
      </div>

      {err ? (
        <div className="text-xs text-accent-red">{err}</div>
      ) : !logs ? (
        <div className="text-xs text-ink-4">加载中…</div>
      ) : logs.length === 0 ? (
        <div className="text-xs text-ink-4">还没有登录记录</div>
      ) : (
        <>
          <ul className="space-y-1.5">
            {(collapsed ? logs.slice(0, 5) : logs).map((l) => (
              <li
                key={l.id}
                className={`flex items-center gap-2 text-xs py-1.5 px-2 rounded ${
                  l.success ? 'bg-bg-2' : 'bg-accent-red/10 border border-accent-red/30'
                }`}
              >
                {l.success ? (
                  <CheckCircle2 size={12} className="text-accent-green shrink-0" />
                ) : (
                  <XCircle size={12} className="text-accent-red shrink-0" />
                )}
                <span className="font-mono text-ink-1 shrink-0">{l.ip || '—'}</span>
                <span className="text-ink-3 shrink-0">
                  {new Date(l.createdAt).toLocaleString('zh-CN', { hour12: false })}
                </span>
                <span className="text-ink-4 truncate" title={l.userAgent}>
                  {shortUserAgent(l.userAgent)}
                </span>
              </li>
            ))}
          </ul>
          {logs.length > 5 && (
            <button
              onClick={() => setCollapsed((c) => !c)}
              className="text-xs text-brand-300 hover:text-brand-200"
            >
              {collapsed ? `展开剩余 ${logs.length - 5} 条` : '收起'}
            </button>
          )}
        </>
      )}
    </div>
  );
}

// shortUserAgent turns a verbose UA string into a one-glance label.
// Best-effort heuristics — UA strings are unstandardised, but we just
// want "macOS / Chrome 124"-shaped output for the timeline.
function shortUserAgent(ua: string): string {
  if (!ua) return '';
  let os = '其它';
  if (/Mac OS X/i.test(ua)) os = 'macOS';
  else if (/Windows/i.test(ua)) os = 'Windows';
  else if (/Linux/i.test(ua)) os = 'Linux';
  else if (/Android/i.test(ua)) os = 'Android';
  else if (/iPhone|iPad/i.test(ua)) os = 'iOS';
  let app = '';
  if (/Electron/i.test(ua)) app = 'DFCHAT';
  else if (/Edg\//i.test(ua)) app = 'Edge';
  else if (/Chrome\//i.test(ua)) app = 'Chrome';
  else if (/Firefox\//i.test(ua)) app = 'Firefox';
  else if (/Safari\//i.test(ua)) app = 'Safari';
  return app ? `${os} · ${app}` : os;
}

function DevicesTab() {
  const [sessions, setSessions] = useState<SessionItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    try {
      setSessions(await listSessions());
    } catch (err: any) {
      setError(err.message ?? '加载失败');
    }
  }

  useEffect(() => { load(); }, []);

  async function revoke(s: SessionItem) {
    if (s.isCurrent) {
      if (!confirm('这是当前设备，撤销后会立即退出登录，确定吗？')) return;
    } else {
      if (!confirm(`确定下线设备 "${s.device || '未知设备'}" 吗？`)) return;
    }
    try {
      await revokeSession(s.id);
      toast('设备已下线', 'success');
      await load();
    } catch (err: any) {
      toast(err.message ?? '操作失败', 'error');
    }
  }

  async function revokeOthers() {
    const otherCount = (sessions ?? []).filter((s) => !s.isCurrent).length;
    if (otherCount === 0) {
      toast('当前只有这台设备在线', 'info');
      return;
    }
    if (!confirm(`将下线其它 ${otherCount} 个会话，确定吗？`)) return;
    try {
      const n = await revokeOtherSessions();
      toast(`已下线 ${n} 个会话`, 'success');
      await load();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    }
  }

  return (
    <section className="card p-6 space-y-4 anim-fade">
      <div className="flex items-center">
        <div>
          <h2 className="text-base font-semibold">登录设备</h2>
          <p className="text-xs text-ink-3 mt-1">查看你账号在哪些设备上保持登录。可强制其它设备下线。</p>
        </div>
        <button onClick={revokeOthers} className="btn-secondary ml-auto">
          <LogOut size={14} /> 下线其它设备
        </button>
      </div>

      {error && <div className="text-sm text-accent-red">{error}</div>}
      {sessions === null && <div className="text-sm text-ink-4">加载中…</div>}
      {sessions && sessions.length === 0 && (
        <div className="text-sm text-ink-4">没有活跃的会话</div>
      )}
      <div className="divide-y divide-bg-5/30">
        {sessions?.map((s) => (
          <div key={s.id} className="py-3 flex items-center gap-3">
            <Smartphone size={20} className="text-ink-3 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="font-medium text-ink-1 truncate">{s.device || '未知设备'}</span>
                {s.isCurrent && (
                  <span className="text-[11px] px-1.5 py-0.5 rounded bg-accent-green/15 text-accent-green">当前设备</span>
                )}
              </div>
              <div className="text-[11px] text-ink-4 mt-0.5">
                登录于 {new Date(s.createdAt).toLocaleString()} · 最近活跃 {relative(s.lastUsedAt)}
              </div>
              <div className="text-[10px] text-ink-4">session #{s.id}</div>
            </div>
            <button onClick={() => revoke(s)} className="btn-secondary text-accent-red hover:bg-accent-red/15">
              <Trash2 size={14} /> 下线
            </button>
          </div>
        ))}
      </div>
    </section>
  );
}

// ArchiveTab — local encrypted message archive panel.
//
// Surface area:
//   - Stats: row count, oldest message date, DB file size on disk
//   - Privacy explanation (which the user explicitly asked for)
//   - "Export all" button (writes plaintext JSON to a user-chosen path)
//   - "Import from previous device" button (merges a prior export)
//
// Only renders meaningfully when running inside Electron — in the
// browser-mode dev build, window.dfchatArchive is undefined, so we
// show a placeholder explaining the desktop client is the archive
// host.
function ArchiveTab() {
  const archive = (typeof window !== 'undefined' && window.dfchatArchive) || undefined;
  const [stats, setStats] = useState<{
    rows: number;
    earliestCreatedAt: string | null;
    latestCreatedAt: string | null;
    dbBytes: number;
  } | null>(null);
  const [busy, setBusy] = useState<'export' | 'import' | null>(null);

  useEffect(() => {
    if (!archive) return;
    let cancelled = false;
    archive.stats().then((s) => { if (!cancelled) setStats(s); }).catch(() => {});
    return () => { cancelled = true; };
  }, [archive]);

  async function refreshStats() {
    if (!archive) return;
    try { setStats(await archive.stats()); } catch { /* ignore */ }
  }

  async function doExport() {
    if (!archive) return;
    setBusy('export');
    try {
      const r = await archive.export();
      if (r.ok) toast(`已导出 ${r.count} 条到 ${r.path}`, 'success');
    } catch (e: any) {
      toast(e?.message ?? '导出失败', 'error');
    } finally {
      setBusy(null);
    }
  }

  async function doImport() {
    if (!archive) return;
    if (!confirm('导入会把选定文件里的消息合并进当前归档（按消息 id 去重）。继续？')) return;
    setBusy('import');
    try {
      const r = await archive.import();
      if (r.ok) {
        toast(`已导入 ${r.count} 条`, 'success');
        await refreshStats();
      }
    } catch (e: any) {
      toast(e?.message ?? '导入失败', 'error');
    } finally {
      setBusy(null);
    }
  }

  if (!archive) {
    return (
      <section className="card p-6 space-y-3 anim-fade">
        <h2 className="text-base font-semibold flex items-center gap-2">
          <Database size={16} /> 本地归档
        </h2>
        <p className="text-sm text-ink-3">
          浏览器环境下没有本地归档。请使用桌面客户端，所有聊天记录会加密存储在本地数据库里。
        </p>
      </section>
    );
  }

  const sizeMB = stats ? (stats.dbBytes / 1024 / 1024).toFixed(1) : '—';

  return (
    <section className="card p-6 space-y-5 anim-fade">
      <div>
        <h2 className="text-base font-semibold flex items-center gap-2">
          <Database size={16} /> 本地归档
        </h2>
        <p className="text-xs text-ink-3 mt-1">
          聊天记录的主力存储在<strong>这台电脑</strong>上 · 服务端只保留最近 30 天 · 30 天后服务端不能改你这里的副本
        </p>
      </div>

      {/* Privacy / threat-model explanation. The user explicitly asked
          for "protect against theft" so call it out plainly. */}
      <div className="rounded-lg border border-bg-5/40 bg-bg-2 p-3 text-xs text-ink-3 space-y-1.5">
        <div className="flex items-center gap-1.5 text-ink-2 font-medium">
          <ShieldCheck size={14} className="text-accent-green" /> 加密说明
        </div>
        <div>
          • 每条消息的正文都用 AES-256-GCM 加密后才写入磁盘
        </div>
        <div>
          • 加密密钥由系统的钥匙串保管（macOS Keychain / Windows DPAPI / Linux libsecret），不在数据库文件里
        </div>
        <div>
          • 仅靠拷走本地 db 文件无法读出内容 — 攻击者还需要登录这台电脑的系统账号
        </div>
        <div>
          • 服务器上 30 天后的消息已经清空，只剩这里的副本
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3 text-sm">
        <div className="rounded-lg bg-bg-2 p-3">
          <div className="text-[11px] text-ink-4">消息条数</div>
          <div className="text-lg font-semibold mt-1">{stats?.rows ?? '—'}</div>
        </div>
        <div className="rounded-lg bg-bg-2 p-3">
          <div className="text-[11px] text-ink-4">最早消息</div>
          <div className="text-sm mt-1">
            {stats?.earliestCreatedAt
              ? new Date(stats.earliestCreatedAt).toLocaleDateString()
              : '—'}
          </div>
        </div>
        <div className="rounded-lg bg-bg-2 p-3">
          <div className="text-[11px] text-ink-4">数据库大小</div>
          <div className="text-sm mt-1">{sizeMB} MB</div>
        </div>
      </div>

      <div className="flex flex-wrap gap-2">
        <button onClick={doExport} disabled={busy !== null} className="btn-secondary">
          {busy === 'export' ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
          导出全部聊天记录
        </button>
        <button onClick={doImport} disabled={busy !== null} className="btn-secondary">
          {busy === 'import' ? <Loader2 size={14} className="animate-spin" /> : <Upload size={14} />}
          从旧设备导入
        </button>
        <button onClick={refreshStats} disabled={busy !== null} className="btn-ghost">
          刷新统计
        </button>
      </div>

      <div className="text-[11px] text-ink-4 leading-relaxed">
        提示：导出的 JSON 文件是<strong>明文</strong>（方便迁移和审计），请保存在加密磁盘 / 加密压缩包里 · 换设备时把它拷到新机器再用「从旧设备导入」即可继承全部历史
      </div>
    </section>
  );
}

function AboutTab() {
  const [checkResult, setCheckResult] = useState<null | {
    state: 'idle' | 'checking' | 'latest' | 'available' | 'error';
    latest?: string;
    downloadUrl?: string;
    notes?: string;
    err?: string;
  }>({ state: 'idle' });

  async function check() {
    if (!window.electronAPI?.checkForUpdates) return;
    setCheckResult({ state: 'checking' });
    try {
      const r = await window.electronAPI.checkForUpdates();
      if (r.available) {
        setCheckResult({ state: 'available', latest: r.latest, downloadUrl: r.downloadUrl, notes: r.notes });
      } else {
        setCheckResult({ state: 'latest', latest: r.latest });
      }
    } catch (e: any) {
      setCheckResult({ state: 'error', err: e?.message ?? '检查失败' });
    }
  }

  return (
    <section className="card p-6 space-y-5 anim-fade">
      <div>
        <h2 className="text-base font-semibold">关于东风快信</h2>
        <p className="text-xs text-ink-3 mt-1">PC 端聊天 + 直播桌面客户端</p>
      </div>

      {/* Version block — big, prominent. Useful when reporting bugs. */}
      <div className="rounded-lg border border-bg-5/40 bg-bg-2 p-4">
        <div className="flex items-center justify-between">
          <div>
            <div className="text-xs text-ink-3">当前版本</div>
            <div className="text-2xl font-mono font-semibold text-ink-1 mt-1">
              v{__APP_VERSION__}
            </div>
          </div>
          {window.electronAPI?.checkForUpdates && (
            <button
              onClick={check}
              disabled={checkResult?.state === 'checking'}
              className="btn-secondary text-xs"
            >
              {checkResult?.state === 'checking' ? (
                <><Loader2 size={12} className="animate-spin" /> 检查中…</>
              ) : (
                <>立即检查更新</>
              )}
            </button>
          )}
        </div>
        {checkResult?.state === 'latest' && (
          <div className="mt-3 text-xs text-accent-green flex items-center gap-1.5">
            <CheckCircle2 size={12} /> 已是最新版本
            {checkResult.latest && <span className="text-ink-4 ml-1">（v{checkResult.latest}）</span>}
          </div>
        )}
        {checkResult?.state === 'available' && (
          <div className="mt-3 p-3 rounded-md bg-brand-500/10 border border-brand-500/40">
            <div className="text-xs text-brand-200 flex items-center gap-1.5">
              发现新版本 <span className="font-mono font-semibold">v{checkResult.latest}</span>
            </div>
            {checkResult.notes && (
              <div className="text-xs text-ink-3 mt-1.5 leading-relaxed">{checkResult.notes}</div>
            )}
            <button
              onClick={() => window.electronAPI?.installUpdate({ downloadUrl: checkResult.downloadUrl })}
              className="btn-primary text-xs mt-3"
            >
              立即下载
            </button>
          </div>
        )}
        {checkResult?.state === 'error' && (
          <div className="mt-3 text-xs text-accent-red">检查失败：{checkResult.err}</div>
        )}
      </div>

      {/* System info */}
      <dl className="text-sm space-y-2">
        <div className="flex justify-between">
          <dt className="text-ink-3">客户端运行环境</dt>
          <dd className="text-ink-1">{window.electronAPI ? `Electron ${window.electronAPI.version}` : '浏览器'}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-ink-3">系统平台</dt>
          <dd className="text-ink-1 font-mono">{window.electronAPI?.platform ?? navigator.platform}</dd>
        </div>
        <div className="flex justify-between">
          <dt className="text-ink-3">服务端</dt>
          <dd className="text-ink-1 font-mono text-xs truncate max-w-[260px]">{import.meta.env.VITE_API_BASE ?? 'http://localhost:8080'}</dd>
        </div>
      </dl>

      {window.electronAPI?.openLogsFolder && (
        <div className="pt-2 border-t border-bg-5/30">
          <button
            onClick={() => window.electronAPI?.openLogsFolder?.()}
            className="btn-secondary text-xs"
            title="出错时把这个文件夹里的 crash-*.log 发给我们排查"
          >
            打开日志文件夹
          </button>
          <p className="text-[11px] text-ink-4 mt-2">
            如果客户端崩溃或出现异常，日志会自动写到这个文件夹。报 bug 时附带最新的 crash-*.log 文件能帮我们快速定位问题。
          </p>
        </div>
      )}
      <p className="text-xs text-ink-4 pt-4 border-t border-bg-5/30">
        © 2026 东方信息 · 东风快信. 本软件部分开源组件版权归各自作者所有。
      </p>
    </section>
  );
}
