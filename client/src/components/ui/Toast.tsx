import { useEffect } from 'react';
import { createPortal } from 'react-dom';
import { create } from 'zustand';
import { CheckCircle2, AlertTriangle, Info, XCircle } from 'lucide-react';

type ToastKind = 'success' | 'error' | 'info' | 'warn';

interface ToastItem {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastStore {
  items: ToastItem[];
  push: (msg: string, kind?: ToastKind) => void;
  dismiss: (id: number) => void;
}

let nextId = 1;

export const useToastStore = create<ToastStore>((set) => ({
  items: [],
  push: (message, kind = 'info') => {
    const id = nextId++;
    set((s) => ({ items: [...s.items, { id, kind, message }] }));
    setTimeout(() => set((s) => ({ items: s.items.filter((i) => i.id !== id) })), 4000);
  },
  dismiss: (id) => set((s) => ({ items: s.items.filter((i) => i.id !== id) })),
}));

export function toast(msg: string, kind: ToastKind = 'info') {
  useToastStore.getState().push(msg, kind);
}

function Icon({ kind }: { kind: ToastKind }) {
  const cls = 'shrink-0';
  if (kind === 'success') return <CheckCircle2 size={18} className={`${cls} text-accent-green`} />;
  if (kind === 'error') return <XCircle size={18} className={`${cls} text-accent-red`} />;
  if (kind === 'warn') return <AlertTriangle size={18} className={`${cls} text-accent-amber`} />;
  return <Info size={18} className={`${cls} text-brand-300`} />;
}

export function Toaster() {
  const items = useToastStore((s) => s.items);
  const dismiss = useToastStore((s) => s.dismiss);

  useEffect(() => {}, []);

  return createPortal(
    <div className="fixed top-5 left-1/2 -translate-x-1/2 z-[60] flex flex-col gap-2 pointer-events-none">
      {items.map((t) => (
        <div
          key={t.id}
          className="anim-slide pointer-events-auto card shadow-pop px-4 py-2.5 flex items-center gap-3 min-w-[260px] max-w-[420px]"
          onClick={() => dismiss(t.id)}
        >
          <Icon kind={t.kind} />
          <span className="text-sm text-ink-1">{t.message}</span>
        </div>
      ))}
    </div>,
    document.body,
  );
}
