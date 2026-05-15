import { useEffect, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  size?: 'sm' | 'md' | 'lg';
}

export default function Modal({ open, onClose, title, children, footer, size = 'sm' }: ModalProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;

  const width = size === 'sm' ? 'max-w-sm' : size === 'lg' ? 'max-w-2xl' : 'max-w-md';
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-sm anim-fade"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className={`card w-full ${width} anim-scale shadow-pop`}>
        <div className="flex items-center px-5 py-3.5 border-b border-bg-5/50">
          <h2 className="font-semibold text-ink-1">{title}</h2>
          <button onClick={onClose} className="btn-icon ml-auto" aria-label="关闭">
            <X size={18} />
          </button>
        </div>
        <div className="px-5 py-4 space-y-3">{children}</div>
        {footer && (
          <div className="px-5 py-3 border-t border-bg-5/50 flex justify-end gap-2 bg-bg-2/40">
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
