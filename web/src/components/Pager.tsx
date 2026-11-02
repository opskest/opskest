// Client-side paging for lists that were fetched in one go: a hook that slices
// the rows, and a control ("Showing 1-25 of 140", previous / next, page size).
// Every list that can grow renders through this (or a server-side "load more")
// so no screen ever draws an unbounded number of rows.

import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { Button } from './button'

export const PAGE_SIZES = [25, 50, 100] as const

export interface Paging<T> {
  /** The rows to render for the current page. */
  rows: T[]
  page: number
  pageSize: number
  total: number
  pageCount: number
  setPage: (page: number) => void
  setPageSize: (size: number) => void
}

/**
 * Slices `items` into pages. The page snaps back to 1 when `resetKey` changes
 * (a filter or search edit) and is clamped when the list shrinks (a refresh
 * or a delete) so you never land on an empty page.
 */
export function usePaging<T>(items: readonly T[], opts: { pageSize?: number; resetKey?: unknown } = {}): Paging<T> {
  const [page, setPageState] = useState(1)
  const [pageSize, setPageSizeState] = useState<number>(opts.pageSize ?? PAGE_SIZES[0])
  const total = items.length
  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const safePage = Math.min(page, pageCount)

  useEffect(() => { setPageState(1) }, [opts.resetKey])

  const rows = useMemo(
    () => items.slice((safePage - 1) * pageSize, safePage * pageSize),
    [items, safePage, pageSize],
  )

  return {
    rows, page: safePage, pageSize, total, pageCount,
    setPage: p => setPageState(Math.min(Math.max(1, p), pageCount)),
    setPageSize: s => { setPageSizeState(s); setPageState(1) },
  }
}

/**
 * The paging control. Renders nothing when everything fits on one page at the
 * smallest page size (and there is nothing more to load), so short lists stay
 * uncluttered.
 *
 * `hasMore` / `onLoadMore` cover lists whose server has more rows than are
 * loaded: the count then reads "of 200+" and a "Load older" button appends.
 */
export function Pager({
  paging, noun = 'rows', hasMore = false, onLoadMore, loadingMore = false, className = '', sizes = PAGE_SIZES,
}: {
  paging: Pick<Paging<unknown>, 'page' | 'pageSize' | 'total' | 'pageCount' | 'setPage' | 'setPageSize'>
  noun?: string
  hasMore?: boolean
  onLoadMore?: () => void
  loadingMore?: boolean
  className?: string
  /** Page sizes offered in the selector (default 25 / 50 / 100); a list of tall rows can start smaller. */
  sizes?: readonly number[]
}) {
  const { page, pageSize, total, pageCount, setPage, setPageSize } = paging
  const sizeId = useId()
  // After "Load older" brings in more rows, step onto the first new page so
  // the click visibly does something.
  const advanceFrom = useRef<number | null>(null)
  useEffect(() => {
    if (advanceFrom.current == null || loadingMore) return
    if (total > advanceFrom.current) setPage(page + 1)
    advanceFrom.current = null
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [total, loadingMore])
  if (total <= sizes[0] && !hasMore) return null

  const from = total === 0 ? 0 : (page - 1) * pageSize + 1
  const to = Math.min(page * pageSize, total)
  const totalLabel = `${total}${hasMore ? '+' : ''}`
  const onLast = page >= pageCount

  return (
    <nav
      aria-label={`${noun} pagination`}
      className={`flex flex-wrap items-center gap-x-4 gap-y-2 text-[12px] text-muted ${className}`}
    >
      <p role="status" aria-live="polite" className="tabular-nums">
        Showing {from}-{to} of {totalLabel}
      </p>

      <div className="flex items-center gap-2 sm:ml-auto">
        <label htmlFor={sizeId} className="whitespace-nowrap">{noun} per page</label>
        <select
          id={sizeId}
          value={pageSize}
          onChange={e => setPageSize(Number(e.target.value))}
          className="bg-ground border border-default text-body rounded-[10px] h-9 px-2 text-[12px] cursor-pointer
            hover:border-strong focus:outline-none focus:border-accent"
        >
          {(sizes.includes(pageSize) ? sizes : [...sizes, pageSize].sort((a, b) => a - b)).map(n => <option key={n} value={n}>{n}</option>)}
        </select>
      </div>

      <div className="flex items-center gap-2">
        <Button size="sm" onClick={() => setPage(page - 1)} disabled={page <= 1} aria-label="Previous page">
          Previous
        </Button>
        <span className="tabular-nums whitespace-nowrap" aria-hidden="true">{page} / {pageCount}</span>
        <Button
          size="sm"
          onClick={() => setPage(page + 1)}
          disabled={onLast}
          aria-label="Next page"
        >
          Next
        </Button>
        {hasMore && onLoadMore && onLast && (
          <Button size="sm" busy={loadingMore} onClick={() => { advanceFrom.current = total; onLoadMore() }}>Load older</Button>
        )}
      </div>
    </nav>
  )
}

/**
 * A short list (an incident's linked monitors, active incidents, …) that shows
 * the first `initial` entries and reveals `step` more per click, so even a
 * list that "should" be small can never flood the page.
 */
export function useShowMore<T>(items: readonly T[], initial = 10, step = 10, resetKey?: unknown) {
  const [count, setCount] = useState(initial)
  useEffect(() => { setCount(initial) }, [resetKey, initial])
  const shown = useMemo(() => items.slice(0, count), [items, count])
  const more = useCallback(() => setCount(c => c + step), [step])
  return { shown, hidden: Math.max(0, items.length - shown.length), more, step }
}

export function ShowMoreButton({ hidden, step = 10, onClick, noun = 'more' }: { hidden: number; step?: number; onClick: () => void; noun?: string }) {
  if (hidden <= 0) return null
  return (
    <Button size="sm" onClick={onClick} className="self-start">
      Show {Math.min(step, hidden)} {noun} ({hidden} hidden)
    </Button>
  )
}
