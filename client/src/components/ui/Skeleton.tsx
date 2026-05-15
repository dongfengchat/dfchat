interface SkeletonProps {
  className?: string;
  rounded?: 'sm' | 'md' | 'lg' | 'full';
}

export default function Skeleton({ className = '', rounded = 'md' }: SkeletonProps) {
  const r = rounded === 'full' ? 'rounded-full' : rounded === 'lg' ? 'rounded-lg' : rounded === 'sm' ? 'rounded' : 'rounded-md';
  return <div className={`skeleton ${r} ${className}`} />;
}

export function ChatSkeleton() {
  return (
    <div className="p-4 space-y-4">
      {Array.from({ length: 6 }).map((_, i) => {
        const mine = i % 3 === 0;
        const w = ['w-2/3', 'w-1/2', 'w-3/5'][i % 3];
        return (
          <div key={i} className={`flex ${mine ? 'justify-end' : 'justify-start'}`}>
            <div className={`flex items-end gap-2 max-w-[70%] ${mine ? 'flex-row-reverse' : ''}`}>
              {!mine && <Skeleton rounded="full" className="w-9 h-9" />}
              <div className="space-y-1.5">
                {!mine && <Skeleton className="h-2.5 w-20" />}
                <Skeleton rounded="lg" className={`h-8 ${w}`} />
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function SidebarRowSkeleton() {
  return (
    <div className="px-3 py-2.5 flex items-center gap-3">
      <Skeleton rounded="full" className="w-9 h-9" />
      <div className="flex-1 space-y-2">
        <Skeleton className="h-3 w-32" />
        <Skeleton className="h-2 w-20" />
      </div>
    </div>
  );
}
