// OutageNotes — the note thread of one outage: every note with its author and
// time ("(edited)" once changed), an inline editor and a delete confirmation for
// the notes the signed-in user may change (their own, or any for an admin), and
// an "Add note" box. An outage takes as many notes as the team needs (up to the
// server's cap), so the cause, the workaround and the follow-up each get their own.
//
//   <OutageNotes outageId={id} onChange={notes => …} />

import { useCallback, useEffect, useMemo, useState } from 'react'
import { ShowMoreButton, useShowMore } from './Pager'
import { api, errorText } from '../lib/api'
import { dateServer } from '../lib/format'
import { normOutageNote } from '../lib/normalize'
import type { OutageNote } from '../lib/types'
import { Alert, Button, ConfirmModal, DropdownMenu, ErrorRetry, Icon, SkeletonRows, Textarea, Timestamp, modKey, useToast } from './ui'

const MAX_LEN = 2000
// The server's cap per outage; the thread is fetched in one request.
const MAX_NOTES = 200

export function OutageNotes({
  outageId, onChange, onConfirmOpen, className = '',
}: {
  outageId: number
  /** Tells a surrounding dialog that the delete confirmation is open, so its Escape closes only that. */
  onConfirmOpen?: (open: boolean) => void
  /** Called with the whole thread after a note is added, edited or removed. */
  onChange?: (notes: OutageNote[]) => void
  className?: string
}) {
  const { push } = useToast()
  const [notes, setNotes] = useState<OutageNote[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [editId, setEditId] = useState<number | null>(null)
  const [editDraft, setEditDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [editError, setEditError] = useState<string | null>(null)
  const [removing, setRemoving] = useState<OutageNote | null>(null)

  const load = useCallback(() => {
    setError(null)
    return api.get<unknown>(`/api/outages/${outageId}/notes?limit=${MAX_NOTES}`)
      .then(d => {
        const list = d && typeof d === 'object' && Array.isArray((d as { notes?: unknown }).notes) ? (d as { notes: unknown[] }).notes : []
        setNotes(list.map(normOutageNote))
      })
      .catch(err => setError(errorText(err, 'Could not load the notes.')))
  }, [outageId])

  useEffect(() => { setNotes(null); void load() }, [load])
  useEffect(() => { onConfirmOpen?.(removing != null) }, [removing, onConfirmOpen])
  useEffect(() => () => onConfirmOpen?.(false), [onConfirmOpen])

  function commit(next: OutageNote[]) {
    setNotes(next)
    onChange?.(next)
  }

  async function add() {
    const body = draft.trim()
    if (!body || adding) return
    setAdding(true)
    setAddError(null)
    try {
      const n = normOutageNote(await api.post<unknown>(`/api/outages/${outageId}/notes`, { body }))
      commit([...(notes ?? []), n])
      setDraft('')
      push('Note added.', 'ok')
    } catch (err) {
      setAddError(errorText(err, 'Could not add that note.'))
    } finally {
      setAdding(false)
    }
  }

  function startEdit(n: OutageNote) {
    setEditId(n.id)
    setEditDraft(n.body)
    setEditError(null)
  }

  async function saveEdit() {
    const body = editDraft.trim()
    if (editId == null || !body || saving) return
    setSaving(true)
    setEditError(null)
    try {
      const n = normOutageNote(await api.put<unknown>(`/api/outages/${outageId}/notes/${editId}`, { body }))
      commit((notes ?? []).map(x => (x.id === n.id ? n : x)))
      setEditId(null)
      push('Note saved.', 'ok')
    } catch (err) {
      setEditError(errorText(err, 'Could not save that note.'))
    } finally {
      setSaving(false)
    }
  }

  async function remove() {
    if (!removing) return
    const id = removing.id
    try {
      await api.del(`/api/outages/${outageId}/notes/${id}`)
      commit((notes ?? []).filter(x => x.id !== id))
      if (editId === id) setEditId(null)
      setRemoving(null)
      push('Note deleted.', 'ok')
    } catch (err) {
      push(errorText(err, 'Could not delete that note.'), 'danger')
      setRemoving(null)
    }
  }

  const full = (notes?.length ?? 0) >= MAX_NOTES
  // The latest notes are shown; earlier ones are revealed ten at a time.
  const latestFirst = useMemo(() => [...(notes ?? [])].reverse(), [notes])
  const more = useShowMore(latestFirst, 10, 10, outageId)
  const visibleNotes = useMemo(() => [...more.shown].reverse(), [more.shown])

  return (
    <div className={`flex flex-col gap-3 ${className}`}>
      {error ? <ErrorRetry message={error} onRetry={() => void load()} /> : notes == null ? <SkeletonRows rows={2} label="Loading notes" framed={false} /> : (
        <>
          {notes.length === 0
            ? <p className="text-[13px] text-muted">No notes yet. Write down what happened, what you did and what follows.</p>
            : (
              <ol className="flex flex-col divide-y divide-default rounded-lg border border-default" aria-label="Outage notes">
                {visibleNotes.map((n, i) => (
                  <li key={n.id} className="flex flex-col gap-1.5 px-3 py-2.5" data-note-id={n.id}>
                    <div className="flex items-center gap-2 min-w-0">
                      <p className="min-w-0 flex-1 text-[12px] text-muted">
                        <span className="font-medium text-body-2">{n.authorName || 'Someone'}</span>
                        {' · '}
                        <Timestamp at={n.createdAt} />
                        {n.editedAt != null && (
                          <span className="text-faint" title={`Edited ${dateServer(n.editedAt)}`}> (edited<span className="sr-only"> {dateServer(n.editedAt)}</span>)</span>
                        )}
                      </p>
                      {n.canEdit && editId !== n.id && (
                        <DropdownMenu
                          label={`Actions for note ${i + 1}`}
                          items={[
                            { key: 'edit', label: 'Edit', icon: <Icon.Edit className="h-3.5 w-3.5" />, onSelect: () => startEdit(n) },
                            { key: 'delete', label: 'Delete', icon: <Icon.Trash className="h-3.5 w-3.5" />, tone: 'danger', onSelect: () => setRemoving(n) },
                          ]}
                        />
                      )}
                    </div>
                    {editId === n.id ? (
                      <div className="flex flex-col gap-2">
                        {editError && <Alert tone="danger">{editError}</Alert>}
                        <Textarea
                          value={editDraft}
                          autoFocus
                          rows={3}
                          aria-label={`Edit note ${i + 1}`}
                          onChange={e => setEditDraft(e.target.value.slice(0, MAX_LEN))}
                          onKeyDown={e => {
                            if (e.key === 'Escape') { e.stopPropagation(); setEditId(null) }
                            else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void saveEdit() }
                          }}
                        />
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="text-[11px] text-faint tnum">{editDraft.length}/{MAX_LEN} · {modKey()} + Enter to save</span>
                          <span className="flex gap-2">
                            <Button size="sm" variant="secondary" onClick={() => setEditId(null)} disabled={saving}>Cancel</Button>
                            <Button size="sm" variant="primary" onClick={() => void saveEdit()} busy={saving} disabled={!editDraft.trim() || editDraft.trim() === n.body}>Save</Button>
                          </span>
                        </div>
                      </div>
                    ) : (
                      <p className="text-[13px] text-body whitespace-pre-line break-words">{n.body}</p>
                    )}
                  </li>
                ))}
              </ol>
            )}
          <ShowMoreButton hidden={more.hidden} step={more.step} onClick={more.more} noun="earlier notes" />

          {full
            ? <p className="text-[12px] text-warn">This outage has the maximum of {MAX_NOTES} notes. Delete one to add another.</p>
            : (
              <div className="flex flex-col gap-2">
                {addError && <Alert tone="danger">{addError}</Alert>}
                <Textarea
                  value={draft}
                  rows={3}
                  aria-label="Add a note"
                  onChange={e => setDraft(e.target.value.slice(0, MAX_LEN))}
                  onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void add() } }}
                  placeholder={notes.length === 0
                    ? 'e.g. Database failover during the AWS maintenance window; traffic moved to the replica.'
                    : 'Add another note: a workaround, a follow-up, who you called…'}
                />
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-[11px] text-faint tnum">Saved with your name · {draft.length}/{MAX_LEN} · {modKey()} + Enter</span>
                  <Button size="sm" variant="primary" icon={<Icon.Plus className="w-3.5 h-3.5" />} onClick={() => void add()} busy={adding} disabled={!draft.trim()}>Add note</Button>
                </div>
              </div>
            )}
        </>
      )}

      <ConfirmModal
        open={removing != null}
        title="Delete this note?"
        description={removing ? <span className="block max-h-32 overflow-y-auto whitespace-pre-line break-words">{removing.body}</span> : undefined}
        confirmLabel="Delete note"
        onConfirm={remove}
        onClose={() => setRemoving(null)}
      />
    </div>
  )
}

/** A "Notes (N)" disclosure that opens an outage's thread in place: for lists of
 * outages that are not tables (the monitor and project outage timelines). */
export function OutageNotesToggle({ outageId, count: initial, latest: initialLatest }: { outageId: number; count: number; latest?: string | null }) {
  const [open, setOpen] = useState(false)
  const [count, setCount] = useState(initial)
  const [latest, setLatest] = useState(initialLatest ?? null)
  return (
    <div className="mt-3 flex flex-col gap-2">
      {!open && latest && <p className="text-sm text-body-2 whitespace-pre-line break-words line-clamp-2"><span className="text-muted">Latest note: </span>{latest}</p>}
      <div>
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(o => !o)}
          className="inline-flex items-center gap-1.5 text-xs font-medium text-accent hover:underline"
        >
          <Icon.Edit className="w-3.5 h-3.5" aria-hidden="true" />
          {open ? 'Hide notes' : count > 0 ? `Notes (${count})` : 'Add note'}
        </button>
      </div>
      {open && (
        <OutageNotes
          outageId={outageId}
          onChange={notes => { setCount(notes.length); setLatest(notes[notes.length - 1]?.body ?? null) }}
        />
      )}
    </div>
  )
}
