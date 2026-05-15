import { useEffect, useState } from 'react';
import { Download, FileText, Image as ImageIcon, Loader2 } from 'lucide-react';
import { listConversationFiles, type ConvFile } from '@/api/client';
import Modal from './ui/Modal';

interface Props {
  open: boolean;
  onClose: () => void;
  /** Conversation id ("g_<groupId>" / "c_<channelId>" / "p_a_b"). */
  conversationId: string;
  title: string;
}

function fmtSize(b?: number): string {
  if (!b) return '';
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
  return `${(b / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

export default function GroupFilesPanel({ open, onClose, conversationId, title }: Props) {
  const [files, setFiles] = useState<ConvFile[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setFiles(null);
    setError(null);
    listConversationFiles(conversationId)
      .then((items) => setFiles(items))
      .catch((err) => setError(err.message ?? '加载失败'));
  }, [open, conversationId]);

  return (
    <Modal open={open} onClose={onClose} title={`${title} · 文件`} size="md">
      {error && <div className="text-sm text-accent-red mb-2">{error}</div>}
      {files === null && (
        <div className="py-8 text-sm text-ink-4 text-center flex items-center justify-center gap-2">
          <Loader2 size={14} className="animate-spin" /> 加载中…
        </div>
      )}
      {files && files.length === 0 && (
        <div className="py-8 text-sm text-ink-4 text-center">这个会话还没有图片或文件</div>
      )}
      {files && files.length > 0 && (
        <div className="max-h-[60vh] overflow-y-auto -mx-5">
          {files.map((f) => (
            <a
              key={f.id}
              href={f.url}
              download={f.name}
              className="flex items-center gap-3 px-5 py-2.5 hover:bg-bg-3/50 transition-colors"
            >
              <div className="w-10 h-10 rounded-lg bg-bg-3 flex items-center justify-center shrink-0 overflow-hidden">
                {f.type === 'image' ? (
                  f.thumbnail || f.url ? (
                    <img src={f.thumbnail || f.url} className="w-full h-full object-cover" alt="" />
                  ) : (
                    <ImageIcon size={18} className="text-ink-3" />
                  )
                ) : (
                  <FileText size={18} className="text-ink-3" />
                )}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm truncate text-ink-1">{f.name || '(无名称)'}</div>
                <div className="text-[11px] text-ink-4">
                  {fmtSize(f.size)} · {new Date(f.createdAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}
                </div>
              </div>
              <Download size={14} className="text-ink-4 shrink-0" />
            </a>
          ))}
        </div>
      )}
    </Modal>
  );
}
