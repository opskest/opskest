// Cursor pagination for the `?before=<ts>&limit=` list endpoints: first
// page on mount (and whenever `key` changes), "Load older" appends the next
// page, `refresh` re-reads page one and keeps older pages already shown.

import { useCallback, useEffect, useRef, useState } from 'react'
import { errorText, hasMorePages, type PageOf } from '../lib/api'

export interface Paged<T> {
  rows: T[] | null
  error: string | null
  hasMore: boolean
  loadingMore: boolean
  loadMore: () => Promise<void>
  reload: () => void
  /** Re-read page one and merge it in front of what's loaded. */
  refresh: () => void
  /** Patch rows in place (after an ack, a note, …). */
  setRows: (update: (rows: T[]) => T[]) => void
}

export function usePaged<T>({
  key, limit, fetchPage, cursorOf, idOf, onLoadMoreError,
}: {
  /** Changing this resets to page one. */
  key: string
  limit: number
  fetchPage: (before?: number) => Promise<PageOf<T>>
  /** The row's timestamp the next `before` is taken from. */
  cursorOf: (row: T) => number
  idOf: (row: T) => number | string
  onLoadMoreError?: (message: string) => void
}): Paged<T> {
  const [rows, setRowsState] = useState<T[] | null>(null)
  const rowsRef = useRef<T[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [cursor, setCursor] = useState<number | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [nonce, setNonce] = useState(0)
  const fetchRef = useRef(fetchPage)
  fetchRef.current = fetchPage
  const cursorOfRef = useRef(cursorOf)
  cursorOfRef.current = cursorOf

  const put = useCallback((next: T[] | null) => {
    rowsRef.current = next
    setRowsState(next)
  }, [])

  const applyCursor = useCallback((page: PageOf<T>, all: T[]) => {
    setCursor(page.nextBefore ?? (all.length ? cursorOfRef.current(all[all.length - 1]) : null))
    setHasMore(hasMorePages(page, limit))
  }, [limit])

  useEffect(() => {
    let alive = true
    put(null)
    setError(null)
    fetchRef.current()
      .then(page => {
        if (!alive) return
        put(page.rows)
        applyCursor(page, page.rows)
      })
      .catch(err => { if (alive) setError(errorText(err, 'Failed to load')) })
    return () => { alive = false }
  }, [key, nonce, put, applyCursor])

  const loadMore = useCallback(async () => {
    if (cursor == null) return
    setLoadingMore(true)
    try {
      const page = await fetchRef.current(cursor)
      const have = new Set((rowsRef.current ?? []).map(idOf))
      const merged = [...(rowsRef.current ?? []), ...page.rows.filter(r => !have.has(idOf(r)))]
      put(merged)
      applyCursor(page, merged)
    } catch (err) {
      onLoadMoreError?.(errorText(err, 'Failed to load older rows'))
    } finally {
      setLoadingMore(false)
    }
  }, [cursor, idOf, put, applyCursor, onLoadMoreError])

  const refresh = useCallback(() => {
    if (rowsRef.current == null) return
    fetchRef.current()
      .then(page => {
        const fresh = page.rows
        const freshIds = new Set(fresh.map(idOf))
        const oldest = fresh.length ? cursorOfRef.current(fresh[fresh.length - 1]) : Number.POSITIVE_INFINITY
        const tail = (rowsRef.current ?? []).filter(r => !freshIds.has(idOf(r)) && cursorOfRef.current(r) <= oldest)
        const merged = [...fresh, ...tail]
        put(merged)
        if (tail.length === 0) applyCursor(page, merged)
      })
      .catch(() => { /* keep what's shown */ })
  }, [idOf, put, applyCursor])

  const setRows = useCallback((update: (rows: T[]) => T[]) => {
    if (rowsRef.current) put(update(rowsRef.current))
  }, [put])

  return { rows, error, hasMore, loadingMore, loadMore, reload: () => setNonce(n => n + 1), refresh, setRows }
}
