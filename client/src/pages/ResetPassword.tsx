import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Eye, EyeOff, KeyRound, Loader2 } from 'lucide-react';
import { resetPassword } from '@/api/client';
import { toast } from '@/components/ui/Toast';

export default function ResetPassword() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const token = params.get('token') ?? '';
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [show, setShow] = useState(false);
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);

  useEffect(() => {
    if (!token) {
      toast('重置链接缺少 token', 'error');
    }
  }, [token]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (password.length < 8) {
      toast('密码至少 8 位', 'error');
      return;
    }
    if (password !== confirm) {
      toast('两次密码不一致', 'error');
      return;
    }
    setLoading(true);
    try {
      await resetPassword(token, password);
      setDone(true);
      toast('密码已重置，请用新密码登录', 'success');
      setTimeout(() => navigate('/login', { replace: true }), 1500);
    } catch (err: any) {
      toast(err.message ?? '重置失败，链接可能已过期', 'error');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-bg-1 px-4">
      <div className="card w-full max-w-md p-8 space-y-5 anim-fade">
        <div>
          <h1 className="text-xl font-semibold flex items-center gap-2">
            <KeyRound size={20} className="text-brand-300" /> 设置新密码
          </h1>
          <p className="text-sm text-ink-3 mt-1">为账号设置一个新密码，至少 8 位。</p>
        </div>

        {!token ? (
          <div className="card p-4 bg-accent-red/10 border border-accent-red/30 text-sm">
            链接无效：缺少 token 参数。请回到 <Link to="/forgot-password" className="text-brand-300 underline">忘记密码</Link> 重新发起。
          </div>
        ) : done ? (
          <div className="card p-4 bg-accent-green/10 border border-accent-green/30 text-sm text-center">
            ✅ 重置成功，正在跳转到登录…
          </div>
        ) : (
          <form onSubmit={submit} className="space-y-4">
            <div className="field">
              <label className="text-xs text-ink-3 block mb-1">新密码</label>
              <div className="relative">
                <input
                  type={show ? 'text' : 'password'}
                  className="input pr-10"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  minLength={8}
                  required
                  autoFocus
                />
                <button
                  type="button"
                  onClick={() => setShow((v) => !v)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 btn-icon w-7 h-7"
                  aria-label={show ? '隐藏密码' : '显示密码'}
                >
                  {show ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
            </div>
            <div className="field">
              <label className="text-xs text-ink-3 block mb-1">确认密码</label>
              <input
                type={show ? 'text' : 'password'}
                className="input"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                minLength={8}
                required
              />
            </div>
            <button type="submit" disabled={loading} className="btn-primary w-full">
              {loading && <Loader2 size={14} className="animate-spin" />}
              重置密码
            </button>
            <Link to="/login" className="block text-center text-sm text-ink-3 hover:text-ink-1">
              取消，返回登录
            </Link>
          </form>
        )}
      </div>
    </div>
  );
}
