import { useMemo, useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Check, Dices, Eye, EyeOff, Loader2, MessageSquareText, RefreshCw, X as XIcon } from 'lucide-react';
import { drawAccountNumbers, login, refreshAccountNumbers, register } from '@/api/client';
import { toast } from '@/components/ui/Toast';
import { useUserStore } from '@/store/userStore';
import TitleBar from '@/components/TitleBar';

// Mirror server-side rules so the user sees red instantly.
const USERNAME_RE = /^[a-zA-Z0-9_]{5,32}$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const TRIVIAL_PASSWORDS = new Set([
  'password', 'password1', 'password123', 'qwerty', 'qwerty123', '12345678',
  '123456789', '1234567890', 'abcdefgh', 'abcd1234', '11111111', '00000000',
  'iloveyou', 'admin123', 'letmein', 'welcome', 'monkey', 'dragon',
  'dfchat', 'dfchat123', 'woaini1314', '5201314', 'qaz123', 'asd123',
]);
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

type Step = 'form' | 'pick';

export default function Register() {
  const navigate = useNavigate();
  const setSession = useUserStore((s) => s.setSession);

  // Step 1 (default) is the form — basic info first. The "选择账号"
  // moment becomes the reward at the end (progressive disclosure: ask
  // for the unfamiliar / committal step last, when the user already
  // has skin in the game).
  const [step, setStep] = useState<Step>('form');

  // Step 2: account-number picking
  const [numbers, setNumbers] = useState<string[]>([]);
  const [selectionToken, setSelectionToken] = useState('');
  const [refreshesLeft, setRefreshesLeft] = useState(0);
  const [pickedNo, setPickedNo] = useState<string | null>(null);
  const [drawing, setDrawing] = useState(false);
  const [drawError, setDrawError] = useState<string | null>(null);

  // Step 1: form. `website` is a honeypot field — always empty for real
  // users (off-screen, aria-hidden, tab-index -1). The server silently
  // 200s a fake response when it arrives non-empty, so naive bots that
  // iterate every named input think they succeeded.
  const [form, setForm] = useState({ username: '', email: '', password: '', confirmPassword: '', nickname: '', website: '' });
  const [showConfirmPassword, setShowConfirmPassword] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [touched, setTouched] = useState<Partial<Record<keyof typeof form, boolean>>>({});

  // No auto-draw on mount — wait until the user actually proceeds to
  // the pick step. Saves wasted reservations on people who bounce off
  // the form. Also: per-IP 24h draw cap is real money for the user, so
  // don't burn one before they've decided to register.

  async function doDraw() {
    setDrawing(true);
    setDrawError(null);
    try {
      const res = await drawAccountNumbers();
      setNumbers(res.numbers);
      setSelectionToken(res.selectionToken);
      setRefreshesLeft(res.refreshesLeft);
      setPickedNo(null);
    } catch (e: any) {
      setDrawError(e?.message ?? '获取号码失败，请刷新页面重试');
    } finally {
      setDrawing(false);
    }
  }

  async function doRefresh() {
    if (!selectionToken || refreshesLeft <= 0) return;
    setDrawing(true);
    setDrawError(null);
    try {
      const res = await refreshAccountNumbers(selectionToken);
      setNumbers(res.numbers);
      setRefreshesLeft(res.refreshesLeft);
      setPickedNo(null);
    } catch (e: any) {
      setDrawError(e?.message ?? '换一批失败');
    } finally {
      setDrawing(false);
    }
  }

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

  // Form step is allowed to proceed once the local validators pass.
  const formStepOk = usernameOk && emailCheck.ok && pwAllOk && confirmOk;
  // Final commit step requires a picked number plus the form-step gates.
  const canSubmit = formStepOk && pickedNo !== null && !loading;

  // proceedToPick — clicked on the form step. Validates locally, kicks
  // off the first draw, and transitions to the pick screen. Loading
  // state covers the draw round-trip so the button shows a spinner.
  async function proceedToPick(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!formStepOk) {
      setTouched({ username: true, email: true, password: true, confirmPassword: true });
      return;
    }
    setLoading(true);
    try {
      await doDraw();
      setStep('pick');
    } finally {
      setLoading(false);
    }
  }

  // confirmRegister — clicked on the pick step. Sends the whole payload
  // (form fields + chosen number + selection token).
  async function confirmRegister() {
    setError(null);
    if (!canSubmit) return;
    setLoading(true);
    try {
      await register({
        username: form.username.trim(),
        email: form.email.trim(),
        password: form.password,
        nickname: form.nickname.trim() || form.username.trim(),
        accountNo: pickedNo!,
        selectionToken,
        website: form.website, // honeypot — always "" for real users
      });
      try {
        const res = await login(form.username.trim(), form.password);
        setSession(res.user, res.accessToken, res.refreshToken);
        toast(`欢迎！你的账号是 ${pickedNo}。请去邮箱点击验证链接。`, 'success');
        navigate('/home', { replace: true });
      } catch {
        toast('注册成功，请登录', 'success');
        navigate('/login', { replace: true });
      }
    } catch (err: any) {
      setError(err.message ?? '注册失败');
      // If the chosen number is gone (race), refresh the picker so the
      // user keeps the form data and just picks a new number.
      if (err?.message?.includes('账号') || err?.message?.includes('摇号')) {
        void doDraw();
      } else if (err?.message?.includes('用户名') || err?.message?.includes('邮箱')) {
        // Validation hit at server — send them back to the form to fix.
        setStep('form');
      }
    } finally {
      setLoading(false);
    }
  }

  return (
    // h-screen so the TitleBar (with the macOS window-drag region) stays
    // pinned; the form is scrollable inside its own bounded container if
    // the password requirement list pushes it past the viewport on a
    // short window.
    <div className="h-screen auth-bg flex flex-col">
      <TitleBar title="东风快信" />
      <div className="flex-1 min-h-0 overflow-y-auto flex items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="flex items-center justify-center gap-2 mb-6">
          <span className="w-10 h-10 rounded-xl bg-brand-500 flex items-center justify-center">
            <MessageSquareText size={22} className="text-white" />
          </span>
          <span className="text-2xl font-semibold tracking-tight">东风快信</span>
        </div>

        {step === 'form' ? (
          <form onSubmit={proceedToPick} className="card p-7 anim-fade shadow-pop space-y-4">
            {/* Honeypot — off-screen text input that real users never see
                or fill. Naive bots iterate every named input; the server
                fake-succeeds when this comes back non-empty. */}
            <input
              type="text"
              name="website"
              tabIndex={-1}
              aria-hidden="true"
              autoComplete="off"
              value={form.website}
              onChange={(e) => set('website', e.target.value)}
              style={{ position: 'absolute', left: '-9999px', width: 1, height: 1, opacity: 0 }}
            />
            <div className="text-center">
              <h1 className="text-xl font-semibold">创建你的 DFCHAT 账号</h1>
              <p className="text-xs text-ink-3 mt-1">
                <span className="text-brand-300 font-medium">第 1 步</span> / 2 · 填写资料
              </p>
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
              <div className="relative">
                <input
                  className="input pr-10"
                  type={showConfirmPassword ? 'text' : 'password'}
                  placeholder="再输一遍上面的密码"
                  value={form.confirmPassword}
                  onChange={(e) => set('confirmPassword', e.target.value)}
                  onBlur={() => markTouched('confirmPassword')}
                  required
                  minLength={8}
                  maxLength={72}
                  autoComplete="new-password"
                />
                <button
                  type="button"
                  onClick={() => setShowConfirmPassword((v) => !v)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 p-1.5 rounded-md text-ink-4 hover:text-ink-1 hover:bg-bg-3 transition-colors"
                  aria-label={showConfirmPassword ? '隐藏密码' : '显示密码'}
                  tabIndex={-1}
                >
                  {showConfirmPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
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

            <button type="submit" disabled={!formStepOk || loading} className="btn-primary w-full">
              {loading && <Loader2 size={16} className="animate-spin" />}
              {loading ? '正在抽号…' : '下一步：选择账号'}
            </button>

            <p className="text-sm text-center text-ink-3">
              已有账号？{' '}
              <Link to="/login" className="text-brand-300 hover:text-brand-200 font-medium">
                去登录
              </Link>
            </p>
          </form>
        ) : (
          // ===== Step 2: pick a number =====
          <div className="card p-7 anim-fade shadow-pop space-y-5">
            <div className="text-center">
              <h1 className="text-xl font-semibold flex items-center justify-center gap-2">
                <Dices size={20} className="text-brand-300" /> 挑一个属于你的账号
              </h1>
              <p className="text-xs text-ink-3 mt-1">
                <span className="text-brand-300 font-medium">第 2 步</span> / 2 · 选好后即创建
              </p>
            </div>

            {error && (
              <div className="text-sm text-accent-red bg-accent-red/10 border border-accent-red/40 rounded-lg px-3 py-2">
                {error}
              </div>
            )}
            {drawError && (
              <div className="text-sm text-accent-red bg-accent-red/10 border border-accent-red/40 rounded-lg px-3 py-2">
                {drawError}
              </div>
            )}

            {drawing && numbers.length === 0 ? (
              <div className="py-10 flex items-center justify-center gap-2 text-ink-3">
                <Loader2 size={16} className="animate-spin" /> 正在摇号…
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-2.5">
                {numbers.map((n) => {
                  const active = pickedNo === n;
                  return (
                    <button
                      key={n}
                      onClick={() => setPickedNo(n)}
                      disabled={drawing}
                      className={`relative py-3 rounded-lg border font-mono text-base tracking-wider transition-all ${
                        active
                          ? 'bg-brand-500 border-brand-500 text-white shadow-md scale-[1.02]'
                          : 'bg-bg-2 border-bg-3 text-ink-2 hover:border-brand-400 hover:bg-bg-3'
                      }`}
                    >
                      {n}
                      {active && (
                        <Check size={14} className="absolute top-1.5 right-1.5" />
                      )}
                    </button>
                  );
                })}
              </div>
            )}

            <button
              onClick={doRefresh}
              disabled={drawing || refreshesLeft <= 0}
              className="btn-secondary text-sm w-full"
              title={refreshesLeft <= 0 ? '刷新次数已用完，请从当前 10 个中选' : ''}
            >
              {drawing ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}
              {refreshesLeft > 0 ? `换一批（还可换 ${refreshesLeft} 次）` : '换一批已用完'}
            </button>

            <div className="flex items-center justify-between gap-3 pt-2">
              <button
                type="button"
                onClick={() => setStep('form')}
                disabled={loading}
                className="btn-ghost text-sm flex-1"
              >
                ← 返回修改资料
              </button>
              <button
                type="button"
                onClick={confirmRegister}
                disabled={!canSubmit}
                className="btn-primary text-sm flex-[1.4]"
              >
                {loading && <Loader2 size={14} className="animate-spin" />}
                {loading ? '创建中…' : pickedNo ? `确认创建 ${pickedNo}` : '请先选一个号'}
              </button>
            </div>

            <div className="text-[11px] text-ink-4 leading-relaxed border-t border-bg-3 pt-3">
              · 号码 10 分钟内有效，超时需重新抽号<br />
              · 部分靓号已被保留 · 后续可能开放
            </div>
          </div>
        )}

        <p className="text-center text-xs text-ink-4 mt-6">© 东方信息 · 东风快信</p>
      </div>
      </div>
    </div>
  );
}
