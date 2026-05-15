import { useEffect, useRef, useState } from 'react';

// Curated set keeps the bundle tiny — a real emoji-mart would add ~350KB.
// Cover the chat-frequent ones; users wanting more can paste from OS picker.
const EMOJI_GROUPS: { name: string; emojis: string[] }[] = [
  {
    name: '常用',
    emojis: ['😀', '😂', '🤣', '😊', '😍', '😘', '😎', '🤔', '🙂', '😅', '😭', '😢', '😡', '🥺', '😴', '🤤', '🤯', '😱', '🤫', '🤗'],
  },
  {
    name: '手势',
    emojis: ['👍', '👎', '👌', '✌️', '🤞', '🤟', '🤘', '🫶', '👏', '🙏', '💪', '🤝', '👋', '🤙', '☝️', '👇', '👉', '👈', '🫡', '🫢'],
  },
  {
    name: '心情',
    emojis: ['❤️', '🧡', '💛', '💚', '💙', '💜', '🖤', '🤍', '💔', '💕', '💖', '💗', '💘', '💝', '💯', '🔥', '✨', '⭐', '🌟', '💫'],
  },
  {
    name: '物品',
    emojis: ['📱', '💻', '⌨️', '🖱️', '📷', '🎮', '🎧', '🎤', '🎬', '🎨', '📝', '📚', '✏️', '🔑', '🔒', '🔧', '⚙️', '💡', '🎁', '🛒'],
  },
  {
    name: '其它',
    emojis: ['☕', '🍵', '🍔', '🍕', '🍣', '🍰', '🍺', '🍷', '🌹', '🌸', '🌍', '🏠', '🚀', '✈️', '🎯', '🎉', '🎊', '🏆', '🐶', '🐱'],
  },
];

interface EmojiPickerProps {
  onPick: (emoji: string) => void;
  onClose: () => void;
}

export default function EmojiPicker({ onPick, onClose }: EmojiPickerProps) {
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onDocClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  return (
    <div
      ref={ref}
      className="card shadow-pop anim-scale w-72 max-h-80 flex flex-col"
      onMouseDown={(e) => e.stopPropagation()}
    >
      <div className="flex border-b border-bg-5/40">
        {EMOJI_GROUPS.map((g, i) => (
          <button
            key={g.name}
            onClick={() => setActive(i)}
            className={`flex-1 py-1.5 text-xs ${active === i ? 'text-ink-1 border-b-2 border-brand-500' : 'text-ink-3 hover:text-ink-1'}`}
          >
            {g.name}
          </button>
        ))}
      </div>
      <div className="grid grid-cols-8 gap-1 p-2 overflow-y-auto">
        {EMOJI_GROUPS[active].emojis.map((e) => (
          <button
            key={e}
            onClick={() => onPick(e)}
            className="aspect-square rounded hover:bg-bg-3 text-xl flex items-center justify-center"
            title={e}
          >
            {e}
          </button>
        ))}
      </div>
    </div>
  );
}
