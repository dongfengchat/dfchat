import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Eye, EyeOff, Loader2, MessageSquareText } from 'lucide-react';
import { register } from '@/api/client';
import { toast } from '@/components/ui/Toast';
import TitleBar from '@/components/TitleBar';

export default function Register() {
  const navigate = useNavigate();
  const [form, setForm] = useState({ username: '', email: '', password: '', nickname: '' });
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  function set<K extends keyof typeof form>(key: K, value: string) {
    setForm((s) => ({ ...s, [key]: value }));
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await register({
        username: form.username.trim(),
        email: form.email.trim(),
        password: form.password,
        nickname: form.nickname.trim() || form.username.trim(),
      });
      toast('注册成功，请登录', 'success');
      navigate('/login', { replace: true });
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
            <label className="text-xs text-ink-3">用户名 <span className="text-ink-4">(3-32 字母数字下划线)</span></label>
            <input
              className="input"
              placeholder="alice"
              value={form.username}
              onChange={(e) => set('username', e.target.value)}
              required
              autoFocus
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">邮箱</label>
            <input
              className="input"
              type="email"
              placeholder="you@example.com"
              value={form.email}
              onChange={(e) => set('email', e.target.value)}
              required
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">昵称 <span className="text-ink-4">(可选)</span></label>
            <input
              className="input"
              placeholder="默认与用户名相同"
              value={form.nickname}
              onChange={(e) => set('nickname', e.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs text-ink-3">密码 <span className="text-ink-4">(至少 8 位)</span></label>
            <div className="relative">
              <input
                className="input pr-10"
                type={showPassword ? 'text' : 'password'}
                placeholder="••••••••"
                value={form.password}
                onChange={(e) => set('password', e.target.value)}
                required
                minLength={8}
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
            {loading ? '注册中…' : '注册'}
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
