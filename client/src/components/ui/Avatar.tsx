interface AvatarProps {
  name: string;
  src?: string;
  size?: number;
  online?: boolean | null;
  className?: string;
}

// Generate a stable HSL color from a name so two users look distinct without
// shipping a backend avatar pipeline.
function colorFor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0;
  const hue = Math.abs(h) % 360;
  return `hsl(${hue} 55% 42%)`;
}

function initials(name: string): string {
  const s = name.trim();
  if (!s) return '?';
  // Group of 2 initials max. Works for both Latin and CJK (takes first
  // visible glyph).
  const parts = s.split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return s.slice(0, 1).toUpperCase();
}

export default function Avatar({ name, src, size = 36, online, className = '' }: AvatarProps) {
  const px = `${size}px`;
  return (
    <span className={`relative inline-flex shrink-0 ${className}`} style={{ width: px, height: px }}>
      {src ? (
        <img
          src={src}
          alt=""
          className="rounded-full object-cover"
          style={{ width: px, height: px }}
        />
      ) : (
        <span
          className="rounded-full flex items-center justify-center text-white font-semibold select-none"
          style={{ width: px, height: px, background: colorFor(name), fontSize: size * 0.4 }}
        >
          {initials(name)}
        </span>
      )}
      {online !== null && online !== undefined && (
        <span
          className={`absolute bottom-0 right-0 rounded-full border-2 border-bg-2 ${
            online ? 'bg-accent-green' : 'bg-ink-4'
          }`}
          style={{ width: Math.max(8, size * 0.28), height: Math.max(8, size * 0.28) }}
        />
      )}
    </span>
  );
}
