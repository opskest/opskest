// Skeleton and its presets: the shape of what is loading, in place of a bare
// spinner. Every preset is one polite status region with a screen-reader
// label; the bars themselves are hidden from assistive technology. The pulse
// is a plain opacity fade, which the global reduced-motion rule freezes.

export function Skeleton({ className = '' }: { className?: string }) {
  return <span aria-hidden="true" className={`block rounded-md bg-surface-hover/70 animate-pulse ${className}`} />
}

function Region({ label, className = '', children }: { label: string; className?: string; children: React.ReactNode }) {
  return (
    <div role="status" aria-busy="true" className={className}>
      <span className="sr-only">{label}</span>
      {children}
    </div>
  )
}

/** Rows of a list: a status chip, a title with a sub line, and a trailing figure. */
export function SkeletonRows({ rows = 5, label = 'Loading', framed = true, className = '' }: { rows?: number; label?: string; framed?: boolean; className?: string }) {
  return (
    <Region label={label} className={`${framed ? 'rounded-[14px] border border-default bg-surface divide-y divide-default overflow-hidden' : 'divide-y divide-default'} ${className}`}>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-4 px-5 py-3.5">
          <Skeleton className="h-5 w-16 shrink-0" />
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <Skeleton className={`h-3.5 ${['w-2/5', 'w-1/2', 'w-1/3', 'w-3/5', 'w-2/5'][i % 5]}`} />
            <Skeleton className="h-3 w-3/4 max-w-xs" />
          </div>
          <Skeleton className="hidden h-3.5 w-16 shrink-0 sm:block" />
        </div>
      ))}
    </Region>
  )
}

/** A grid of cards: identity line, a status chip and two lines of text. */
export function SkeletonCards({ count = 6, label = 'Loading', className = 'grid gap-3 sm:grid-cols-2 xl:grid-cols-3' }: { count?: number; label?: string; className?: string }) {
  return (
    <Region label={label} className={className}>
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-[14px] border border-default bg-surface p-4">
          <div className="flex items-center gap-3">
            <Skeleton className="h-9 w-9 shrink-0 rounded-lg" />
            <Skeleton className="h-4 w-1/2" />
          </div>
          <Skeleton className="h-5 w-24" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      ))}
    </Region>
  )
}

/** A table: a header strip and rows of equal cells. */
export function SkeletonTable({ rows = 6, cols = 4, label = 'Loading', className = '' }: { rows?: number; cols?: number; label?: string; className?: string }) {
  return (
    <Region label={label} className={`overflow-hidden rounded-[14px] border border-default bg-surface ${className}`}>
      <div className="flex gap-4 border-b border-default bg-surface-2 px-3 py-3">
        {Array.from({ length: cols }, (_, c) => <Skeleton key={c} className="h-3 flex-1" />)}
      </div>
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex gap-4 border-b border-default px-3 py-3.5 last:border-0">
          {Array.from({ length: cols }, (_, c) => <Skeleton key={c} className={`h-3.5 flex-1 ${c === 0 ? 'max-w-[40%]' : ''}`} />)}
        </div>
      ))}
    </Region>
  )
}

/** A heading block with a few content lines, for a page section. */
export function SkeletonBlock({ lines = 3, label = 'Loading', className = '' }: { lines?: number; label?: string; className?: string }) {
  return (
    <Region label={label} className={`flex flex-col gap-3 ${className}`}>
      <Skeleton className="h-5 w-1/3" />
      {Array.from({ length: lines }, (_, i) => <Skeleton key={i} className={`h-3.5 ${i === lines - 1 ? 'w-2/3' : 'w-full'}`} />)}
    </Region>
  )
}
