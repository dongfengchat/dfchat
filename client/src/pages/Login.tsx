import { useState, type FormEvent } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Eye, EyeOff, Loader2, MessageSquareText } from 'lucide-react';
import { login } from '@/api/client';
import { useUserStore } from '@/store/userStore';
import TitleBar from '@/components/TitleBar';

export default function Login() {
  const navigate = useNavigate();
  const location = useLocation();
  const setSession = useUserStore((s) => s.setSession);
  const [loginValue, setLoginValue] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const expired = new URLSearchParams(location.search).get('expired') === '1';

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const res = await login(loginValue.trim(), password);
      setSession(res.user, res.accessToken, res.refreshToken);
      navigate('/home');
    } catch (err: any) {
      setError(err.message ?? '登录失败');
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

        <form
          onSubmit={onSubmit}
          className="card p-7 anim-fade shadow-pop space-y-4"
        >
          <div className="text-center">
            <h1 className="text-xl font-semibold">欢迎回来</h1>
            <p className="text-sm text-ink-3 mt-1">登录你的账号继续使用</p>
          </div>

          {expired && !error && (
            <div className="text-sm text-accent-amber bg-accent-amber/10 border border-accent-amber/40 rounded-lg px-3 py-2">
              登录已过期，请重新登录。
            </div>
          )}
          {error && (
            <div className="text-sm text-accent-red bg-accent-red/10 border border-accent-red/40 rounded-lg px-3 py-2">
              {error}
            </div>
          )}

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">账号 / 用户名 / 邮箱</label>
            <input
              className="input"
              type="text"
              placeholder="100123 · alice · alice@gmail.com"
              value={loginValue}
              onChange={(e) => setLoginValue(e.target.value)}
              required
              autoFocus
              autoComplete="username"
            />
            <div className="text-[11px] text-ink-4">三种登录方式都支持</div>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">密码</label>
            <div className="relative">
              <input
                className="input pr-10"
                type={showPassword ? 'text' : 'password'}
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
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
          </div>

          <button type="submit" disabled={loading} className="btn-primary w-full">
            {loading && <Loader2 size={16} className="animate-spin" />}
            {loading ? '登录中…' : '登录'}
          </button>

          <div className="flex items-center justify-between text-sm text-ink-3">
            <Link to="/forgot-password" className="text-ink-3 hover:text-brand-300">
              忘记密码？
            </Link>
            <span>
              没有账号？{' '}
              <Link to="/register" className="text-brand-300 hover:text-brand-200 font-medium">
                立即注册
              </Link>
            </span>
          </div>
        </form>
        <p className="text-center text-xs text-ink-4 mt-6">© 东方信息 · 东风快信</p>
      </div>
      </div>
    </div>
  );
}
