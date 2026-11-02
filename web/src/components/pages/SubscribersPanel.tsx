// The page editor's subscriber list (existing pages only): counts, the
// subscribers 50 at a time (server limit/offset) with remove buttons. Slack subscribers show as "Slack
// webhook" — the URL is a secret and never leaves the server.

import { useCallback, useEffect, useState } from 'react'
import { api, errorText } from '../../lib/api'
import { dateServer } from '../../lib/format'
import { Pager, usePaging } from '../Pager'
import { Badge, Button, ErrorRetry, Icon, useToast } from '../ui'

interface Sub { id: number; kind: 'email' | 'slack'; email?: string; confirmedAt: number | null; createdAt: number }
const BATCH = 50
interface Resp { confirmed: number; pending: number; subscribers: Sub[] }

export function SubscribersPanel({ pageId }: { pageId: number }) {
  const toast = useToast()
  const [data, setData] = useState<Resp | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [removing, setRemoving] = useState<number | null>(null)
  const [lastBatch, setLastBatch] = useState(0)
  const [loadingMore, setLoadingMore] = useState(false)
  const load = useCallback(() => {
    setError(null)
    api.get<Resp>(`/api/pages/${pageId}/subscribers?limit=${BATCH}`)
      .then(d => { const subscribers = d.subscribers ?? []; setData({ ...d, subscribers }); setLastBatch(subscribers.length) })
      .catch(e => setError(errorText(e, 'Could not load subscribers.')))
  }, [pageId])
  useEffect(load, [load])
  const paging = usePaging(data?.subscribers ?? [], { resetKey: pageId })

  async function loadMore() {
    if (!data) return
    setLoadingMore(true)
    try {
      const d = await api.get<Resp>(`/api/pages/${pageId}/subscribers?limit=${BATCH}&offset=${data.subscribers.length}`)
      const next = d.subscribers ?? []
      setData(cur => {
        if (!cur) return cur
        const have = new Set(cur.subscribers.map(x => x.id))
        return { ...cur, subscribers: [...cur.subscribers, ...next.filter(x => !have.has(x.id))] }
      })
      setLastBatch(next.length)
    } catch (e) {
      toast.push(errorText(e, 'Could not load more subscribers'), 'danger')
    } finally { setLoadingMore(false) }
  }

  async function remove(s: Sub) {
    if (removing != null) return
    setRemoving(s.id)
    try {
      await api.del(`/api/pages/${pageId}/subscribers/${s.id}`)
      load()
    } catch (e) {
      toast.push(errorText(e, 'Could not remove subscriber'), 'danger')
    } finally { setRemoving(null) }
  }

  if (!data) return error ? <ErrorRetry message={error} onRetry={load} /> : <p role="status" className="text-[13px] text-muted">Loading subscribers…</p>
  return (
    <div className="flex flex-col gap-4 border-t border-default pt-5">
      <h3 className="text-[15px] font-semibold">Subscriber directory</h3>
      {error && <ErrorRetry message={error} onRetry={load} />}
      <p className="text-[13px] text-body-2">
        <strong className="tnum">{data.confirmed}</strong> subscribed
        {data.pending > 0 && <span className="text-muted"> · {data.pending} waiting to confirm (expire after 48 h)</span>}
      </p>
      {data.subscribers.length > 0 && (
        <ul className="flex flex-col divide-y divide-default border border-default bg-surface rounded-[14px] overflow-x-auto">
          {paging.rows.map(s => (
            <li key={s.id} className="flex items-center gap-2 px-3 py-2 text-[13px]">
              {s.kind === 'slack' ? <Badge tone="accent">Slack</Badge> : <Badge tone="neutral">Email</Badge>}
              <span className="flex-1 min-w-0 truncate text-body">{s.kind === 'slack' ? 'Slack webhook' : s.email}</span>
              {s.confirmedAt == null && <Badge tone="warn">Pending</Badge>}
              <span className="text-[11px] text-faint tnum hidden sm:inline">{dateServer(s.createdAt)}</span>
              <Button size="sm" variant="ghost" busy={removing === s.id} disabled={removing != null} aria-label={`Remove ${s.kind === 'slack' ? 'Slack subscriber' : s.email || 'subscriber'}`} onClick={() => remove(s)}><Icon.Trash className="w-3.5 h-3.5" /></Button>
            </li>
          ))}
        </ul>
      )}
      <Pager paging={paging} noun="Subscribers" hasMore={lastBatch >= BATCH} onLoadMore={loadMore} loadingMore={loadingMore} />
      {data.subscribers.length === 0 && <p className="text-[13px] text-muted">No subscribers yet. People can subscribe from the public page when updates are enabled.</p>}
    </div>
  )
}
