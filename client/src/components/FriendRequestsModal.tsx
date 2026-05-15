import { useEffect, useState } from 'react';
import { Check, Loader2, UserPlus, X } from 'lucide-react';
import {
  acceptFriendRequest,
  dropFriendRequest,
  listFriendRequests,
  listFriends,
  type FriendRequest,
} from '@/api/client';
import { useChatStore } from '@/store/chatStore';
import Avatar from './ui/Avatar';
import Modal from './ui/Modal';
import { toast } from './ui/Toast';

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function FriendRequestsModal({ open, onClose }: Props) {
  const setFriends = useChatStore((s) => s.setFriends);
  const [incoming, setIncoming] = useState<FriendRequest[]>([]);
  const [outgoing, setOutgoing] = useState<FriendRequest[]>([]);
  const [loading, setLoading] = useState(false);
  const [tab, setTab] = useState<'in' | 'out'>('in');
  const [busyId, setBusyId] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const r = await listFriendRequests();
      setIncoming(r.incoming);
      setOutgoing(r.outgoing);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (open) load();
  }, [open]);

  async function accept(req: FriendRequest) {
    setBusyId(req.userId);
    try {
      await acceptFriendRequest(req.userId);
      toast(`已添加 ${req.nickname || req.username} 为好友`, 'success');
      // Friends list now includes them.
      setFriends(await listFriends());
      await load();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setBusyId(null);
    }
  }

  async function drop(req: FriendRequest, kind: 'reject' | 'cancel') {
    setBusyId(req.userId);
    try {
      await dropFriendRequest(req.userId);
      toast(kind === 'reject' ? '已忽略请求' : '已取消请求', 'success');
      await load();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setBusyId(null);
    }
  }

  if (!open) return null;
  const list = tab === 'in' ? incoming : outgoing;

  return (
    <Modal open onClose={onClose} title="好友请求" size="md">
      <div className="flex gap-2 border-b border-bg-5/40 -mx-5 px-5 pb-2 -mt-3">
        <button
          onClick={() => setTab('in')}
          className={`px-3 py-1.5 text-sm rounded-md ${tab === 'in' ? 'bg-bg-3 text-ink-1' : 'text-ink-3 hover:bg-bg-3/60'}`}
        >
          收到 {incoming.length > 0 && (
            <span className="ml-1 inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded-full bg-accent-red text-white text-[11px]">{incoming.length}</span>
          )}
        </button>
        <button
          onClick={() => setTab('out')}
          className={`px-3 py-1.5 text-sm rounded-md ${tab === 'out' ? 'bg-bg-3 text-ink-1' : 'text-ink-3 hover:bg-bg-3/60'}`}
        >
          已发出 {outgoing.length > 0 && (
            <span className="ml-1 inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded-full bg-bg-4 text-ink-2 text-[11px]">{outgoing.length}</span>
          )}
        </button>
      </div>

      <div className="-mx-5 max-h-[55vh] overflow-y-auto">
        {loading && <div className="px-5 py-6 text-sm text-ink-4 text-center">加载中…</div>}
        {!loading && list.length === 0 && (
          <div className="px-5 py-10 text-sm text-ink-4 text-center flex flex-col items-center gap-2">
            <UserPlus size={28} className="text-ink-4" />
            {tab === 'in' ? '没有新的好友请求' : '没有待处理的请求'}
          </div>
        )}
        {list.map((r) => (
          <div key={r.userId} className="px-5 py-3 flex items-center gap-3 border-b border-bg-5/20 last:border-0">
            <Avatar name={r.nickname || r.username} size={36} />
            <div className="min-w-0 flex-1">
              <div className="font-medium text-ink-1 truncate">{r.nickname || r.username}</div>
              <div className="text-[11px] text-ink-4 truncate">@{r.username} · {new Date(r.createdAt).toLocaleString()}</div>
            </div>
            {tab === 'in' ? (
              <div className="flex gap-1">
                <button
                  onClick={() => drop(r, 'reject')}
                  disabled={busyId === r.userId}
                  className="btn-secondary"
                  title="忽略"
                >
                  <X size={14} /> 忽略
                </button>
                <button
                  onClick={() => accept(r)}
                  disabled={busyId === r.userId}
                  className="btn-primary"
                >
                  {busyId === r.userId ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}
                  接受
                </button>
              </div>
            ) : (
              <button
                onClick={() => drop(r, 'cancel')}
                disabled={busyId === r.userId}
                className="btn-secondary"
              >
                <X size={14} /> 取消
              </button>
            )}
          </div>
        ))}
      </div>
    </Modal>
  );
}
