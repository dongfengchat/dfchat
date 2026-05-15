import { useRef, useState } from 'react';
import { Loader2, Upload, Trash2, Megaphone } from 'lucide-react';
import { updateGroup, uploadBlob } from '@/api/client';
import Modal from './ui/Modal';
import { toast } from './ui/Toast';
import type { Group } from '@/types';

interface Props {
  open: boolean;
  group: Group;
  onClose: () => void;
  onSaved: (g: Group) => void;
  /** Members can only edit announcement; admins can edit description+announcement; only owner can edit name/icon. */
  myRole: number;
}

export default function EditGroupDialog({ open, group, onClose, onSaved, myRole }: Props) {
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description ?? '');
  const [announcement, setAnnouncement] = useState(group.announcement ?? '');
  const [iconUrl, setIconUrl] = useState(group.iconUrl ?? '');
  const [uploading, setUploading] = useState(false);
  const [saving, setSaving] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const canEditAll = myRole >= 2;
  const canEditAdmin = myRole >= 1;

  async function pickIcon(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    if (!f.type.startsWith('image/')) {
      toast('请选择图片文件', 'error');
      return;
    }
    setUploading(true);
    try {
      const uploaded = await uploadBlob(f, f.name, 'image');
      setIconUrl(uploaded.url);
      toast('头像已上传', 'success');
    } catch (err: any) {
      toast(err.message ?? '上传失败', 'error');
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  }

  async function save() {
    setSaving(true);
    try {
      const patch: Parameters<typeof updateGroup>[1] = {
        announcement: announcement,
      };
      if (canEditAdmin) {
        patch.description = description;
      }
      if (canEditAll) {
        patch.name = name.trim() || undefined;
        patch.iconUrl = iconUrl;
      }
      const next = await updateGroup(group.id, patch);
      toast('已保存', 'success');
      onSaved(next);
    } catch (e: any) {
      toast(e.message ?? '保存失败', 'error');
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="编辑群组" size="md">
      <div className="space-y-4">
        {canEditAll && (
          <div>
            <label className="text-xs text-ink-3">群头像</label>
            <div className="mt-1 flex items-center gap-3">
              <div className="w-16 h-16 rounded-xl bg-bg-3 overflow-hidden shrink-0 flex items-center justify-center">
                {iconUrl ? (
                  <img src={iconUrl} className="w-full h-full object-cover" alt="" />
                ) : (
                  <span className="text-xl font-semibold text-ink-3">{name[0] || '#'}</span>
                )}
              </div>
              <input type="file" accept="image/*" ref={fileRef} onChange={pickIcon} className="hidden" />
              <button
                onClick={() => fileRef.current?.click()}
                disabled={uploading}
                className="btn-secondary text-xs"
              >
                {uploading ? <Loader2 size={14} className="animate-spin" /> : <Upload size={14} />}
                {iconUrl ? '更换头像' : '上传头像'}
              </button>
              {iconUrl && (
                <button onClick={() => setIconUrl('')} className="btn-icon text-ink-3" title="移除">
                  <Trash2 size={14} />
                </button>
              )}
            </div>
          </div>
        )}

        {canEditAll && (
          <div>
            <label className="text-xs text-ink-3">群名称</label>
            <input className="input mt-1" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} />
          </div>
        )}

        {canEditAdmin && (
          <div>
            <label className="text-xs text-ink-3">群简介</label>
            <textarea
              className="input mt-1 min-h-[60px]"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              maxLength={500}
              placeholder="这个群是做什么的？"
            />
          </div>
        )}

        <div>
          <label className="text-xs text-ink-3 inline-flex items-center gap-1">
            <Megaphone size={11} /> 群公告
          </label>
          <textarea
            className="input mt-1 min-h-[80px]"
            value={announcement}
            onChange={(e) => setAnnouncement(e.target.value)}
            maxLength={2000}
            placeholder="所有群成员进入后都会看到这条公告。"
          />
          <div className="text-[11px] text-ink-4 mt-1">
            {canEditAdmin
              ? '管理员可以修改公告，所有成员能看到。'
              : '只有管理员可以修改公告。'}
          </div>
        </div>

        <div className="flex gap-2 justify-end pt-2 border-t border-bg-5/40">
          <button onClick={onClose} className="btn-secondary">取消</button>
          <button onClick={save} disabled={saving} className="btn-primary">
            {saving ? <Loader2 size={14} className="animate-spin" /> : null} 保存
          </button>
        </div>
      </div>
    </Modal>
  );
}
