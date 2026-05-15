import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowLeft, KeyRound, Loader2, Mail } from 'lucide-react';
import { forgotPassword } from '@/api/client';
import { toast } from '@/components/ui/Toast';

export default function ForgotPassword() {
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [sent, setSent] = useState(false);
  const [devLink, setDevLink] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    setLoading(true);
    try {
      const res = await forgotPassword(email.trim());
      setSent(true);
      if (res.devLink) {
        setDevLink(res.devLink);
        toast('SMTP 未配置 — 重置链接已显示在页面上（开发模式）', 'info');
      } else {
        toast('如果该邮箱已注册，会收到重置邮件', 'success');
      }
    } catch (err: any) {
      toast(err.message ?? '提交失败', 'error');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-bg-1 px-4">
      <div className="card w-full max-w-md p-8 space-y-5 anim-fade">
        <button
          onClick={() => navigate('/login')}
          className="text-sm text-ink-3 hover:text-ink-1 inline-flex items-center gap-1"
        >
          <ArrowLeft size={14} /> 返回登录
        </button>
        <div>
          <h1 className="text-xl font-semibold flex items-center gap-2">
            <KeyRound size={20} className="text-brand-300" /> 找回密码
          </h1>
          <p className="text-sm text-ink-3 mt-1">
            输入注册邮箱，我们会发送一条重置链接到你的邮箱。
          </p>
        </div>

        {!sent ? (
          <form onSubmit={submit} className="space-y-4">
            <div className="field">
              <label className="text-xs text-ink-3 block mb-1">邮箱</label>
              <div className="relative">
                <Mail size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-4" />
                <input
                  type="email"
                  className="input pl-9"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  required
                  autoFocus
                />
              </div>
            </div>
            <button type="submit" disabled={loading || !email.trim()} className="btn-primary w-full">
              {loading && <Loader2 size={14} className="animate-spin" />}
              发送重置邮件
            </button>
          </form>
        ) : (
          <div className="space-y-3">
            <div className="card p-4 bg-accent-green/10 border border-accent-green/30 text-sm">
              ✅ 已提交。如果 <strong>{email}</strong> 是已注册邮箱，几分钟内会收到密码重置邮件。
              <br />
              <span className="text-ink-4 text-xs mt-1 block">没收到？检查垃圾邮件文件夹，或一小时后再来。</span>
            </div>
            {devLink && (
              <div className="card p-3 bg-bg-3 border border-bg-5/40 text-xs">
                <div className="text-ink-3 mb-1">🔧 开发模式 · 重置链接：</div>
                <a href={devLink} className="text-brand-300 break-all underline">{devLink}</a>
              </div>
            )}
            <Link to="/login" className="btn-secondary w-full justify-center">
              返回登录
            </Link>
          </div>
        )}
      </div>
    </div>
  );
}
