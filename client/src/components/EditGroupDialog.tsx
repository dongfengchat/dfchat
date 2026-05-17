import { useRef, useState } from 'react';
import { AlertTriangle, Copy, Globe, Loader2, Lock, Megaphone, RefreshCw, Trash2, Upload } from 'lucide-react';
import { deleteGroup, rotateGroupInviteCode, updateGroup, uploadBlob } from '@/api/client';
import Modal from './ui/Modal';
import { toast } from './ui/Toast';
import type { Group } from '@/types';

interface Props {
  open: boolean;
  group: Group;
  onClose: () => void;
  onSaved: (g: Group) => void;
  /** Owner-only callback fired after a successful dissolve; the parent
   *  should clear the active conversation + drop the group from any
   *  local store. If absent, dissolve is hidden. */
  onDissolved?: (g: Group) => void;
  /** Members can only edit announcement; admins can edit description+announcement; only owner can edit name/icon. */
  myRole: number;
}

export default function EditGroupDialog({ open, group, onClose, onSaved, onDissolved, myRole }: Props) {
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description ?? '');
  const [announcement, setAnnouncement] = useState(group.announcement ?? '');
  const [iconUrl, setIconUrl] = useState(group.iconUrl ?? '');
  const [isPublic, setIsPublic] = useState(!!group.isPublic);
  const [inviteCode, setInviteCode] = useState(group.inviteCode);
  const [uploading, setUploading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [confirmDissolve, setConfirmDissolve] = useState(false);
  const [dissolving, setDissolving] = useState(false);
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
        if (isPublic !== !!group.isPublic) patch.isPublic = isPublic;
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

  async function rotate() {
    if (!confirm('重新生成邀请码后，原邀请码立即失效。已经分享出去的旧链接将无法用于加入。\n\n继续？')) return;
    setRotating(true);
    try {
      const code = await rotateGroupInviteCode(group.id);
      setInviteCode(code);
      onSaved({ ...group, inviteCode: code });
      toast('邀请码已更新', 'success');
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setRotating(false);
    }
  }

  async function copyInvite() {
    try {
      await navigator.clipboard.writeText(inviteCode);
      toast('邀请码已复制', 'success');
    } catch {
      toast('复制失败，请手动选择', 'warn');
    }
  }

  async function dissolve() {
    setDissolving(true);
    try {
      await deleteGroup(group.id);
      toast('群组已解散', 'success');
      onDissolved?.(group);
      onClose();
    } catch (e: any) {
      toast(e.message ?? '解散失败', 'error');
      setDissolving(false);
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

        {/* Invite-code panel — visible to anyone who can see the group
            settings; rotate is admin/owner only. Copy is universal. */}
        <div>
          <label className="text-xs text-ink-3">邀请码</label>
          <div className="mt-1 flex items-center gap-2">
            <input
              className="input flex-1 font-mono tracking-wide"
              value={inviteCode}
              readOnly
              onFocus={(e) => e.target.select()}
            />
            <button onClick={copyInvite} className="btn-secondary text-xs" title="复制邀请码">
              <Copy size={14} /> 复制
            </button>
            {canEditAdmin && (
              <button onClick={rotate} disabled={rotating} className="btn-secondary text-xs" title="重新生成（旧码立即失效）">
                {rotating ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />} 换一个
              </button>
            )}
          </div>
          <div className="text-[11px] text-ink-4 mt-1">
            分享这个码给好友，他们在「加入群组」里输入即可加入。
            {canEditAdmin && '换码后所有已分享出去的旧链接失效。'}
          </div>
        </div>

        {canEditAll && (
          <div>
            <label className="text-xs text-ink-3">公开度</label>
            <div className="mt-1 flex flex-col gap-1.5">
              <label className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  className="mt-1"
                  checked={!isPublic}
                  onChange={() => setIsPublic(false)}
                />
                <span className="flex flex-col">
                  <span className="text-sm inline-flex items-center gap-1"><Lock size={12} /> 仅邀请加入</span>
                  <span className="text-[11px] text-ink-4">只有拿到邀请码的人才能加入。</span>
                </span>
              </label>
              <label className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  className="mt-1"
                  checked={isPublic}
                  onChange={() => setIsPublic(true)}
                />
                <span className="flex flex-col">
                  <span className="text-sm inline-flex items-center gap-1"><Globe size={12} /> 公开可发现</span>
                  <span className="text-[11px] text-ink-4">未来可在「探索群组」里被人搜到。</span>
                </span>
              </label>
            </div>
          </div>
        )}

        <div className="flex gap-2 justify-end pt-2 border-t border-bg-5/40">
          <button onClick={onClose} className="btn-secondary">取消</button>
          <button onClick={save} disabled={saving} className="btn-primary">
            {saving ? <Loader2 size={14} className="animate-spin" /> : null} 保存
          </button>
        </div>

        {/* Danger zone — owner-only manual dissolve. Two-step confirm
            so a misclick doesn't nuke a chatty group full of history. */}
        {canEditAll && onDissolved && (
          <div className="mt-4 rounded-lg border border-accent-red/40 bg-accent-red/5 p-3">
            <div className="text-xs text-accent-red flex items-center gap-1 font-medium">
              <AlertTriangle size={12} /> 危险操作
            </div>
            <div className="text-[11px] text-ink-3 mt-1">
              解散群组后，所有成员立刻失去访问；历史消息会保留在数据库以便审计，但任何人都看不到。
              操作不可撤销。
            </div>
            {!confirmDissolve ? (
              <button
                onClick={() => setConfirmDissolve(true)}
                className="btn-secondary text-xs mt-2 text-accent-red hover:bg-accent-red/10"
              >
                <Trash2 size={12} /> 解散群组…
              </button>
            ) : (
              <div className="mt-2 flex items-center gap-2">
                <button
                  onClick={dissolve}
                  disabled={dissolving}
                  className="btn-primary text-xs bg-accent-red hover:bg-accent-red/90 border-accent-red"
                >
                  {dissolving ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />} 确认解散
                </button>
                <button onClick={() => setConfirmDissolve(false)} className="btn-ghost text-xs">
                  取消
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}
