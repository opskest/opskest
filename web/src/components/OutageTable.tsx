// Outages table: when, duration, what happened (the note
// the on-call person wrote, over the automatic first reason), regions and
// acknowledgement. Acknowledge and the notes are open to every signed-in user
// (viewers may ack and annotate). An outage has a thread of notes
// (OutageNotes), opened from its row; the row shows the newest. Every ended
// outage should end up with a note saying why it happened.
//
//   <OutageTable monitorId={id} />             one monitor's outages
//   <OutageTable filters={…} showMonitor />    /api/outages across monitors
//
// The group and date filters are sent to the server and also applied to
// the rows here, so they work whether or not the server honours them.

import { useCallback, useRef, useState } from 'react'
import { Link } from 'wouter'
import { api, errorText, unwrapPage } from '../lib/api'
import { dateServer, duration } from '../lib/format'
import { normOutage } from '../lib/normalize'
import type { Outage, OutageNote } from '../lib/types'
import { usePaged } from '../hooks/usePaged'
import { Pager, usePaging } from './Pager'
import { HeadRow, ROW, TableFrame, TD } from './table'
import { OutageNotes } from './OutageNotes'
import { Badge, Button, ErrorRetry, Icon, Modal, SkeletonTable, useToast } from './ui'

const LIMIT = 50

export interface OutageFilters {
  active?: boolean
  /** Only outages that have ended. */
  resolvedOnly?: boolean
  monitorId?: number
  groupId?: number
  from?: number
  to?: number
  /** Search: monitor name, reason or note (server-side). */
  q?: string
  note?: 'missing' | 'present'
  /** Minimum duration in minutes. */
  minDuration?: number
}

export function OutageTable({
  monitorId, filters, showMonitor, monitorName, monitorGroup, viewerLabel, onNoted,
}: {
  monitorId?: number
  filters?: OutageFilters
  showMonitor?: boolean
  monitorName?: (id: number) => string
  /** For the client-side group filter. */
  monitorGroup?: (id: number) => number | null | undefined
  viewerLabel: string
  /** Called after a note is saved or removed (to refresh counts). */
  onNoted?: () => void
}) {
  const { push } = useToast()
  const [busyId, setBusyId] = useState<number | null>(null)
  const [noteFor, setNoteFor] = useState<Outage | null>(null)
  // The delete confirmation sits above the notes dialog: Escape closes only it.
  const confirmOpen = useRef(false)
  const setConfirmOpen = useCallback((open: boolean) => { confirmOpen.current = open }, [])

  const f = filters ?? {}
  const lastId = useRef<number | null>(null)
  const endpoint = monitorId != null ? `/api/monitors/${monitorId}/outages` : '/api/outages'

  const paged = usePaged<Outage>({
    key: `${endpoint}|${JSON.stringify(f)}`,
    limit: LIMIT,
    fetchPage: before => {
      const q = new URLSearchParams({ limit: String(LIMIT) })
      if (f.active) q.set('active', 'true')
      else if (f.resolvedOnly) q.set('active', 'false')
      if (f.monitorId != null) q.set('monitorId', String(f.monitorId))
      if (f.groupId != null) q.set('groupId', String(f.groupId))
      if (f.from != null) q.set('from', String(f.from))
      if (f.to != null) q.set('to', String(f.to))
      if (f.q) q.set('q', f.q)
      if (f.note) q.set('note', f.note)
      if (f.minDuration) q.set('minDuration', String(f.minDuration))
      const b = before ?? (f.to != null ? f.to + 1 : undefined)
      if (b != null) q.set('before', String(b))
      // Keyset tie-break: continue after the last row we got, so outages
      // sharing the same start millisecond aren't skipped between pages.
      if (before != null && lastId.current != null) q.set('beforeId', String(lastId.current))
      return api.get<unknown>(`${endpoint}?${q}`).then(d => {
        const p = unwrapPage<unknown>(d, 'outages')
        const rows = p.rows.map(normOutage)
        if (rows.length > 0) lastId.current = rows[rows.length - 1]!.id
        return { rows, nextBefore: p.nextBefore }
      })
    },
    cursorOf: o => o.startedAt,
    idOf: o => o.id,
    onLoadMoreError: m => push(m, 'danger'),
  })

  async function acknowledge(o: Outage) {
    setBusyId(o.id)
    try {
      await api.post(`/api/outages/${o.id}/ack`)
      paged.setRows(rows => rows.map(r => (r.id === o.id ? { ...r, ackedBy: viewerLabel, ackedAt: Date.now() } : r)))
      push('Outage acknowledged — reminders stop for it.', 'ok')
    } catch (err) {
      push(errorText(err, 'Could not acknowledge that outage.'), 'danger')
    } finally {
      setBusyId(null)
    }
  }

  /** The thread changed: keep the row's newest-note summary and count in step. */
  function notesChanged(outageId: number, notes: OutageNote[]) {
    const newest = notes[notes.length - 1]
    paged.setRows(rows => rows.map(o => (o.id === outageId
      ? {
        ...o,
        noteCount: notes.length,
        note: newest?.body ?? null,
        noteBy: newest?.authorName || null,
        noteAt: newest ? (newest.editedAt ?? newest.createdAt) : null,
      }
      : o)))
    onNoted?.()
  }

  // Client-side filters first, then page what is loaded (hooks stay above the
  // early returns).
  const matching = (paged.rows ?? []).filter(o =>
    (!f.active || o.endedAt == null)
    && (!f.resolvedOnly || o.endedAt != null)
    && (f.monitorId == null || o.monitorId === f.monitorId)
    && (f.groupId == null || !monitorGroup || monitorGroup(o.monitorId) === f.groupId)
    && (f.from == null || o.startedAt >= f.from)
    && (f.to == null || o.startedAt <= f.to)
    && (f.note !== 'missing' || !o.note)
    && (f.note !== 'present' || !!o.note))
  const paging = usePaging(matching, { resetKey: `${endpoint}|${JSON.stringify(f)}` })
  const rows = paging.rows
  const last = paged.rows?.[paged.rows.length - 1]
  const more = paged.hasMore && !(f.from != null && last && last.startedAt < f.from)

  if (paged.error) return <ErrorRetry message={paged.error} onRetry={paged.reload} />
  if (paged.rows == null) return <SkeletonTable rows={6} cols={5} label="Loading outages" />

  return (
    <div className="flex flex-col gap-3">
      {matching.length === 0
        ? <p className="text-[13px] text-muted py-4">{more ? 'No matching outages in the loaded range.' : 'No outages recorded.'}</p>
        : (
          <TableFrame scroll minWidth={showMonitor ? 900 : 780}>
            <HeadRow cells={[...(showMonitor ? ['Monitor'] : []), 'When', 'Duration', 'What happened', 'Regions', 'Acknowledged']} />
            <tbody>
              {rows.map(o => {
                const ongoing = o.endedAt == null
                return (
                  <tr key={o.id} className={`${ROW} align-top`}>
                    {showMonitor && (
                      <td className={`${TD} max-w-[200px]`}>
                        <Link href={`/monitors/${o.monitorId}`} className="font-medium text-body hover:text-accent block truncate">
                          {monitorName?.(o.monitorId) ?? `Monitor ${o.monitorId}`}
                        </Link>
                      </td>
                    )}
                    <td className={`${TD} tnum whitespace-nowrap`}>
                      <span className="block text-body-2">{dateServer(o.startedAt)}</span>
                      <span className="block text-[12px] text-faint mt-0.5">
                        {ongoing ? <Badge tone="danger" dot>Ongoing</Badge> : <>until {dateServer(o.endedAt as number)}</>}
                      </span>
                    </td>
                    <td className={`${TD} tnum whitespace-nowrap font-medium text-body-2`}>{duration((o.endedAt ?? Date.now()) - o.startedAt)}</td>
                    <td className={`${TD} min-w-[260px] max-w-[420px]`}>
                      <div className="flex flex-col gap-1.5">
                        {o.note ? (
                          <div className="flex items-start gap-2">
                            <Icon.Edit className="w-3.5 h-3.5 mt-0.5 text-accent shrink-0" />
                            <div className="min-w-0 flex-1">
                              <p className="text-[13px] text-body whitespace-pre-line break-words line-clamp-3">{o.note}</p>
                              <p className="text-[11px] text-faint mt-0.5">
                                {o.noteBy ?? 'Someone'}{o.noteAt ? ` · ${dateServer(o.noteAt)}` : ''}
                                {' · '}
                                <button type="button" onClick={() => setNoteFor(o)} className="text-accent hover:underline"
                                  aria-label={`Notes for this outage, ${o.noteCount} in total`}>
                                  {o.noteCount > 1 ? `All ${o.noteCount} notes` : 'Notes'}
                                </button>
                              </p>
                            </div>
                          </div>
                        ) : (
                          <div className="flex items-center gap-2">
                            <Button size="sm" variant={ongoing ? 'secondary' : 'primary'} onClick={() => setNoteFor(o)}>
                              <Icon.Plus className="w-3.5 h-3.5" /> Add note
                            </Button>
                            {!ongoing && <span className="text-[11px] text-warn">Cause not recorded</span>}
                          </div>
                        )}
                        <p className="text-[12px] text-muted break-words font-mono" title="First failed check">{o.firstReason || '—'}</p>
                        {o.suppressedBy != null && (
                          <span><Badge tone="neutral" title="A parent monitor was down, so no alert was sent for this outage">
                            Alerts held · parent {monitorName?.(o.suppressedBy) ?? `#${o.suppressedBy}`} down
                          </Badge></span>
                        )}
                      </div>
                    </td>
                    <td className={`${TD} whitespace-nowrap`}><RegionsBadge rc={o.regions} /></td>
                    <td className={`${TD} whitespace-nowrap`}>
                      {o.ackedBy
                        ? <Badge tone="accent" title={o.ackedAt ? dateServer(o.ackedAt) : undefined}>{o.ackedBy}</Badge>
                        : ongoing
                          ? <Button size="sm" busy={busyId === o.id} onClick={() => acknowledge(o)}>Acknowledge</Button>
                          : <span className="text-faint">—</span>}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </TableFrame>
        )}
      <Pager paging={paging} noun="Outages" hasMore={more} onLoadMore={paged.loadMore} loadingMore={paged.loadingMore} />

      <Modal
        open={noteFor != null}
        onClose={() => { if (!confirmOpen.current) setNoteFor(null) }}
        title="Outage notes"
        wide
        description={noteFor ? (
          <span className="flex flex-col gap-0.5">
            <span className="font-medium text-body">{showMonitor && monitorName ? monitorName(noteFor.monitorId) : 'This monitor'}</span>
            <span className="tnum">
              {dateServer(noteFor.startedAt)}
              {noteFor.endedAt != null ? ` · ${duration(noteFor.endedAt - noteFor.startedAt)}` : ' · ongoing'}
              {noteFor.firstReason ? ` · ${noteFor.firstReason}` : ''}
            </span>
          </span>
        ) : undefined}
        footer={<Button variant="secondary" onClick={() => setNoteFor(null)}>Done</Button>}
      >
        {noteFor && <OutageNotes outageId={noteFor.id} onConfirmOpen={setConfirmOpen} onChange={notes => notesChanged(noteFor.id, notes)} />}
      </Modal>
    </div>
  )
}

/** Multi-region verdict for an outage: down everywhere = confirmed;
 * reachable elsewhere = likely a problem on this server's side. */
export function RegionsBadge({ rc }: { rc: Outage['regions'] }) {
  if (!rc) return <span className="text-faint">—</span>
  const detail = (rc.probes ?? []).map(p => `${p.country}${p.city ? ` ${p.city}` : ''}: ${p.ok ? `ok${p.code ? ` (${p.code})` : ''} ${p.latencyMs} ms` : p.detail || 'failed'}`).join('\n')
  if (rc.error || rc.total === 0) return <Badge tone="neutral" title={rc.error}>Check failed</Badge>
  if (rc.reachable === 0) return <Badge tone="danger" title={detail}>Down in all {rc.total} regions</Badge>
  if (rc.reachable === rc.total) return <Badge tone="warn" title={detail}>Reachable from all {rc.total}</Badge>
  return <Badge tone="warn" title={detail}>Reachable from {rc.reachable}/{rc.total}</Badge>
}
