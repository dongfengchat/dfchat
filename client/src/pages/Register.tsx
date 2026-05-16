import { useMemo, useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Check, Eye, EyeOff, Loader2, MessageSquareText, X as XIcon } from 'lucide-react';
import { login, register } from '@/api/client';
import { toast } from '@/components/ui/Toast';
import { useUserStore } from '@/store/userStore';
import TitleBar from '@/components/TitleBar';

// Mirror the server-side constants exactly. Keeping them in sync means
// the user only sees the friendly real-time hints and the server's
// validation is the actual authority — no "client says ok then server
// 400s" surprise.
const USERNAME_RE = /^[a-zA-Z0-9_]{5,32}$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// Subset of server's commonPasswords list — covers the truly trivial
// stuff so the user sees red instantly. The server has the full list
// (~70 entries) and stays authoritative.
const TRIVIAL_PASSWORDS = new Set([
  'password', 'password1', 'password123', 'qwerty', 'qwerty123', '12345678',
  '123456789', '1234567890', 'abcdefgh', 'abcd1234', '11111111', '00000000',
  'iloveyou', 'admin123', 'letmein', 'welcome', 'monkey', 'dragon',
  'dfchat', 'dfchat123', 'woaini1314', '5201314', 'qaz123', 'asd123',
]);

// Subset of server's disposable list — top offenders only. Tells the
// user "this won't work" before they even hit submit.
const DISPOSABLE_DOMAINS = new Set([
  'mailinator.com', '10minutemail.com', 'guerrillamail.com', 'yopmail.com',
  'tempmail.com', 'temp-mail.org', 'throwawaymail.com', 'trashmail.com',
  'sharklasers.com', 'maildrop.cc', 'getnada.com', 'dispostable.com',
  'example.com', 'example.org', 'example.net',
]);

type Issue = { ok: boolean; label: string };

function checkPassword(pw: string): Issue[] {
  const lower = pw.toLowerCase();
  const len = pw.length;
  const allSame = pw.length > 0 && [...pw].every((c) => c === pw[0]);
  let hasDigit = false, hasLetter = false, hasSymbol = false;
  for (const c of pw) {
    if (/\d/.test(c)) hasDigit = true;
    else if (/[a-zA-ZÀ-￿]/.test(c)) hasLetter = true;
    else hasSymbol = true;
  }
  const classes = (hasDigit ? 1 : 0) + (hasLetter ? 1 : 0) + (hasSymbol ? 1 : 0);
  return [
    { ok: len >= 8 && len <= 72, label: '长度 8–72 位' },
    { ok: classes >= 2, label: '至少混合两种：字母 / 数字 / 符号' },
    { ok: !allSame, label: '不能全是同一个字符' },
    { ok: pw.length === 0 || !TRIVIAL_PASSWORDS.has(lower), label: '不是常见弱密码' },
  ];
}

function checkEmail(email: string): { ok: boolean; reason?: string } {
  const trimmed = email.trim();
  if (!trimmed) return { ok: false };
  if (!EMAIL_RE.test(trimmed)) return { ok: false, reason: '邮箱格式不正确' };
  if (trimmed.length > 128) return { ok: false, reason: '邮箱过长' };
  const domain = trimmed.split('@').pop()?.toLowerCase() ?? '';
  if (DISPOSABLE_DOMAINS.has(domain)) return { ok: false, reason: '不接受一次性邮箱，请用真实邮箱' };
  return { ok: true };
}

export default function Register() {
  const navigate = useNavigate();
  const setSession = useUserStore((s) => s.setSession);
  const [form, setForm] = useState({ username: '', email: '', password: '', confirmPassword: '', nickname: '' });
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [touched, setTouched] = useState<Partial<Record<keyof typeof form, boolean>>>({});

  function set<K extends keyof typeof form>(key: K, value: string) {
    setForm((s) => ({ ...s, [key]: value }));
  }
  function markTouched(key: keyof typeof form) {
    setTouched((t) => ({ ...t, [key]: true }));
  }

  const pwIssues = useMemo(() => checkPassword(form.password), [form.password]);
  const pwAllOk = pwIssues.every((i) => i.ok);
  const emailCheck = useMemo(() => checkEmail(form.email), [form.email]);
  const usernameOk = USERNAME_RE.test(form.username);
  const confirmOk = form.confirmPassword.length > 0 && form.confirmPassword === form.password;

  // Strength score 0..4: one point each for length≥12, mixed classes, has-symbol, has-digit-and-letter
  const strength = useMemo(() => {
    const pw = form.password;
    if (!pw) return 0;
    let s = 0;
    if (pw.length >= 12) s++;
    let hasD = false, hasL = false, hasS = false;
    for (const c of pw) {
      if (/\d/.test(c)) hasD = true;
      else if (/[a-zA-Z]/.test(c)) hasL = true;
      else hasS = true;
    }
    if (hasD && hasL) s++;
    if (hasS) s++;
    if (pw.length >= 16) s++;
    return s;
  }, [form.password]);

  const canSubmit = usernameOk && emailCheck.ok && pwAllOk && confirmOk && !loading;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    // Final gate (defensive — the button is also disabled). All blocking
    // conditions surface as red checks above so we don't need a generic
    // error here.
    if (!canSubmit) {
      setTouched({ username: true, email: true, password: true, confirmPassword: true });
      return;
    }
    setLoading(true);
    try {
      await register({
        username: form.username.trim(),
        email: form.email.trim(),
        password: form.password,
        nickname: form.nickname.trim() || form.username.trim(),
      });
      // Auto-login so the user doesn't re-type credentials they just
      // typed twice. The server fires a verification email on register
      // (background goroutine); the banner inside the app nudges them
      // to click the link.
      try {
        const res = await login(form.username.trim(), form.password);
        setSession(res.user, res.accessToken, res.refreshToken);
        toast('注册成功！请去邮箱点击验证链接。', 'success');
        navigate('/home', { replace: true });
      } catch {
        // Auto-login failed (rare — e.g. transient db issue between
        // register and login). Fall back to the login page.
        toast('注册成功，请登录', 'success');
        navigate('/login', { replace: true });
      }
    } catch (err: any) {
      setError(err.message ?? '注册失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen auth-bg flex flex-col">
      <TitleBar title="东风快信" />
      <div className="flex-1 flex items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="flex items-center justify-center gap-2 mb-6">
          <span className="w-10 h-10 rounded-xl bg-brand-500 flex items-center justify-center">
            <MessageSquareText size={22} className="text-white" />
          </span>
          <span className="text-2xl font-semibold tracking-tight">东风快信</span>
        </div>

        <form onSubmit={onSubmit} className="card p-7 anim-fade shadow-pop space-y-4">
          <div className="text-center">
            <h1 className="text-xl font-semibold">创建新账号</h1>
            <p className="text-sm text-ink-3 mt-1">加入东风快信开始聊天</p>
          </div>

          {error && (
            <div className="text-sm text-accent-red bg-accent-red/10 border border-accent-red/40 rounded-lg px-3 py-2">
              {error}
            </div>
          )}

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">用户名</label>
            <input
              className="input"
              placeholder="alice123"
              value={form.username}
              onChange={(e) => set('username', e.target.value)}
              onBlur={() => markTouched('username')}
              required
              autoFocus
              autoComplete="username"
            />
            <div className={`text-[11px] ${touched.username && form.username && !usernameOk ? 'text-accent-red' : 'text-ink-4'}`}>
              {touched.username && form.username && !usernameOk
                ? '5–32 位，仅字母 / 数字 / 下划线'
                : '5–32 位字母 / 数字 / 下划线，注册后不可修改'}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">邮箱</label>
            <input
              className="input"
              type="email"
              placeholder="you@gmail.com"
              value={form.email}
              onChange={(e) => set('email', e.target.value)}
              onBlur={() => markTouched('email')}
              required
              autoComplete="email"
            />
            {touched.email && form.email && !emailCheck.ok && emailCheck.reason ? (
              <div className="text-[11px] text-accent-red">{emailCheck.reason}</div>
            ) : (
              <div className="text-[11px] text-ink-4">用于接收验证邮件 / 找回密码 · 一次性邮箱不被允许</div>
            )}
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">昵称 <span className="text-ink-4">(可选)</span></label>
            <input
              className="input"
              placeholder="默认与用户名相同"
              value={form.nickname}
              onChange={(e) => set('nickname', e.target.value)}
              maxLength={64}
              autoComplete="nickname"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">密码</label>
            <div className="relative">
              <input
                className="input pr-10"
                type={showPassword ? 'text' : 'password'}
                placeholder="••••••••"
                value={form.password}
                onChange={(e) => set('password', e.target.value)}
                onBlur={() => markTouched('password')}
                required
                minLength={8}
                maxLength={72}
                autoComplete="new-password"
              />
              <button
                type="button"
                onClick={() => setShowPassword((v) => !v)}
                className="absolute right-2 top-1/2 -translate-y-1/2 p-1.5 rounded-md text-ink-4 hover:text-ink-1 hover:bg-bg-3 transition-colors"
                aria-label={showPassword ? '隐藏密码' : '显示密码'}
                tabIndex={-1}
              >
                {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </div>
            {/* Strength bar — quick visual feedback */}
            <div className="flex gap-1 h-1 mt-1">
              {[0, 1, 2, 3].map((i) => (
                <div
                  key={i}
                  className={`flex-1 rounded ${
                    i < strength
                      ? strength <= 1
                        ? 'bg-accent-red'
                        : strength === 2
                          ? 'bg-accent-amber'
                          : 'bg-accent-green'
                      : 'bg-bg-3'
                  }`}
                />
              ))}
            </div>
            {/* Checklist — turns green as each requirement is met */}
            <ul className="text-[11px] space-y-0.5 mt-1">
              {pwIssues.map((it, i) => (
                <li
                  key={i}
                  className={`flex items-center gap-1.5 ${
                    form.password.length === 0
                      ? 'text-ink-4'
                      : it.ok
                        ? 'text-accent-green'
                        : 'text-accent-red'
                  }`}
                >
                  {form.password.length === 0 ? (
                    <span className="w-3 h-3 rounded-full border border-ink-4" />
                  ) : it.ok ? (
                    <Check size={12} />
                  ) : (
                    <XIcon size={12} />
                  )}
                  {it.label}
                </li>
              ))}
            </ul>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">确认密码</label>
            <input
              className="input"
              type={showPassword ? 'text' : 'password'}
              placeholder="再输一遍上面的密码"
              value={form.confirmPassword}
              onChange={(e) => set('confirmPassword', e.target.value)}
              onBlur={() => markTouched('confirmPassword')}
              required
              minLength={8}
              maxLength={72}
              autoComplete="new-password"
            />
            {touched.confirmPassword && form.confirmPassword && !confirmOk ? (
              <div className="text-[11px] text-accent-red">两次输入的密码不一致</div>
            ) : confirmOk ? (
              <div className="text-[11px] text-accent-green flex items-center gap-1">
                <Check size={12} /> 两次密码一致
              </div>
            ) : (
              <div className="text-[11px] text-ink-4">为防止手误，需要再输一遍</div>
            )}
          </div>

          <button type="submit" disabled={!canSubmit} className="btn-primary w-full">
            {loading && <Loader2 size={16} className="animate-spin" />}
            {loading ? '注册中…' : '注册并登录'}
          </button>

          <p className="text-sm text-center text-ink-3">
            已有账号？{' '}
            <Link to="/login" className="text-brand-300 hover:text-brand-200 font-medium">
              去登录
            </Link>
          </p>
        </form>
        <p className="text-center text-xs text-ink-4 mt-6">© 东方信息 · 东风快信</p>
      </div>
      </div>
    </div>
  );
}
