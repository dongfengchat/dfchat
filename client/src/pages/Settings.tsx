import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  AlertTriangle,
  ArrowLeft,
  Info,
  KeyRound,
  Loader2,
  LogOut,
  Monitor,
  Save,
  Smartphone,
  Trash2,
  UserCircle,
} from 'lucide-react';
import {
  changePassword,
  deleteMe,
  listSessions,
  logoutServer,
  revokeOtherSessions,
  revokeSession,
  updateMe,
  uploadBlob,
} from '@/api/client';
import { useSeqStore } from '@/sync/seqStore';
import { useUserStore } from '@/store/userStore';
import type { SessionItem } from '@/types';
import Avatar from '@/components/ui/Avatar';
import TitleBar from '@/components/TitleBar';
import { toast } from '@/components/ui/Toast';

type Tab = 'profile' | 'security' | 'devices' | 'about';

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

function AboutTab() {
  return (
    <section className="card p-6 space-y-4 anim-fade">
      <div>
        <h2 className="text-base font-semibold">关于东风快信</h2>
        <p className="text-xs text-ink-3 mt-1">PC 端聊天 + 直播桌面客户端</p>
      </div>
      <dl className="text-sm space-y-2">
        <div className="flex justify-between"><dt className="text-ink-3">版本</dt><dd className="text-ink-1">v{__APP_VERSION__}</dd></div>
        <div className="flex justify-between"><dt className="text-ink-3">客户端运行环境</dt><dd className="text-ink-1">{window.electronAPI ? `Electron ${window.electronAPI.version}` : '浏览器'}</dd></div>
        <div className="flex justify-between"><dt className="text-ink-3">服务端</dt><dd className="text-ink-1">{import.meta.env.VITE_API_BASE ?? 'http://localhost:8080'}</dd></div>
      </dl>
      {window.electronAPI?.openLogsFolder && (
        <div className="pt-2">
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
        © 2026 东方信息. 本软件部分开源组件版权归各自作者所有。
      </p>
    </section>
  );
}
