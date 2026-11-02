// Table chrome shared by every data table: a bordered, horizontally
// scrollable frame (so a wide table never widens the page), the header row
// style, and the "Load older" footer.

import type { ReactNode } from 'react'
import { Button } from './button'

/** The list pattern: a 14px container of divided rows (never a stack of floating cards). */
export const LIST_FRAME = 'rounded-[14px] border border-default bg-surface divide-y divide-default overflow-hidden'
/** One row inside a list frame: 20px sides, 14px top and bottom; `relative` so a row can anchor an overlay link. */
export const LIST_ROW = 'relative px-5 py-3.5 transition-colors hover:bg-surface-hover'
/** A list's column-header row. */
export const LIST_HEAD = 'px-5 py-2.5 text-[11px] font-medium uppercase tracking-wide text-faint'

/** A bounded scroll region for the rows of one page of a list: a page of tall rows never stretches the document. */
export const LIST_SCROLL = 'relative max-h-[min(72dvh,46rem)] overflow-y-auto overscroll-contain scroll-slim'

export function ListFrame({ children, className = '', as: As = 'div', label }: { children: ReactNode; className?: string; as?: 'div' | 'section' | 'ul'; label?: string }) {
  return <As aria-label={label} className={`${LIST_FRAME} ${className}`}>{children}</As>
}

export function TableFrame({ children, minWidth = 560, scroll = false }: { children: ReactNode; minWidth?: number; scroll?: boolean }) {
  return (
    <div className={`${scroll ? LIST_SCROLL : 'overflow-x-auto'} border border-default rounded-[14px] bg-surface scroll-slim`}>
      <table className="w-full text-[13px] border-collapse" style={{ minWidth }}>{children}</table>
    </div>
  )
}

export function HeadRow({ cells }: { cells: ReactNode[] }) {
  return (
    <thead>
      <tr className="border-b border-default bg-surface-2 text-left text-[11px] font-semibold text-muted uppercase tracking-wide">
        {cells.map((c, i) => <th key={i} scope="col" className="sticky top-0 z-[1] bg-surface-2 px-3 py-2.5 font-semibold whitespace-nowrap">{c}</th>)}
      </tr>
    </thead>
  )
}

export const ROW = 'border-b border-default last:border-0 hover:bg-surface-hover/50'
export const TD = 'px-3 py-3 align-middle'

export function LoadOlder({ show, busy, onClick, label = 'Load older' }: { show: boolean; busy: boolean; onClick: () => void; label?: string }) {
  if (!show) return null
  return (
    <Button variant="secondary" size="sm" onClick={onClick} busy={busy} className="self-start">
      {label}
    </Button>
  )
}
