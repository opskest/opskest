import { useState, type FormEvent } from 'react'
import { api, errorText } from '../lib/api'
import { normTag } from '../lib/normalize'
import type { Tag } from '../lib/types'
import { Button, ConfirmModal, Icon, Input, useToast } from './ui'

/** A small, readable palette; the first is the default colour. */
export const TAG_COLORS = ['#6b7385', '#2563eb', '#7c3aed', '#db2777', '#dc2626', '#ea580c', '#ca8a04', '#059669']
const HEX = /^#[0-9a-fA-F]{6}$/
const sortTags = (list: Tag[]) => [...list].sort((a, b) => a.name.localeCompare(b.name))
const dot = (color: string) => HEX.test(color) ? color : TAG_COLORS[0]

function Swatches({ value, onChange, label }: { value: string; onChange: (c: string) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap items-center gap-1.5">
      {TAG_COLORS.map(c => (
        <button key={c} type="button" role="radio" aria-checked={value.toLowerCase() === c} aria-label={c} onClick={() => onChange(c)}
          className={`h-6 w-6 rounded-full border-2 transition focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 ${value.toLowerCase() === c ? 'border-body' : 'border-transparent hover:border-strong'}`}
          style={{ backgroundColor: c }} />
      ))}
    </div>
  )
}

/**
 * Create, rename, recolour and delete tags. `usage` is how many monitors use
 * each tag, shown beside it and in the delete warning. `onChange` gets the
 * whole list after every change; `onCreated` the tag that was just made.
 */
export function TagManager({ tags, usage = {}, onChange, onCreated, onDeleted }: {
  tags: Tag[]
  usage?: Record<number, number>
  onChange: (next: Tag[]) => void
  onCreated?: (tag: Tag) => void
  onDeleted?: (id: number) => void
}) {
  const { push } = useToast()
  const [name, setName] = useState('')
  const [color, setColor] = useState(TAG_COLORS[0])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<number | null>(null)
  const [editName, setEditName] = useState('')
  const [editColor, setEditColor] = useState(TAG_COLORS[0])
  const [editError, setEditError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Tag | null>(null)

  async function create(event: FormEvent) {
    event.preventDefault()
    const n = name.trim()
    if (!n) { setError('Give the tag a name.'); return }
    setBusy(true); setError(null)
    try {
      const made = normTag(await api.post('/api/tags', { name: n, color }))
      onChange(sortTags([...tags, made]))
      onCreated?.(made)
      setName('')
    } catch (e) {
      setError(errorText(e, 'Could not create the tag. A tag with that name may already exist.'))
    } finally { setBusy(false) }
  }

  function startEdit(t: Tag) { setEditing(t.id); setEditName(t.name); setEditColor(dot(t.color)); setEditError(null) }

  async function saveEdit(t: Tag) {
    const n = editName.trim()
    if (!n) { setEditError('Give the tag a name.'); return }
    setBusy(true); setEditError(null)
    try {
      const saved = normTag(await api.put(`/api/tags/${t.id}`, { name: n, color: editColor }))
      onChange(sortTags(tags.map(x => x.id === t.id ? saved : x)))
      setEditing(null)
    } catch (e) {
      setEditError(errorText(e, 'Could not save the tag. A tag with that name may already exist.'))
    } finally { setBusy(false) }
  }

  async function remove(t: Tag) {
    await api.del(`/api/tags/${t.id}`)
    onChange(tags.filter(x => x.id !== t.id))
    onDeleted?.(t.id)
    push(`Deleted tag "${t.name}".`, 'ok')
  }

  const inUse = deleting ? usage[deleting.id] ?? 0 : 0

  return (
    <div className="flex flex-col gap-5">
      <form onSubmit={create} className="flex flex-col gap-3 rounded-[12px] border border-default bg-surface-2/40 p-4" aria-label="New tag">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
          <div className="min-w-0 flex-1">
            <Input aria-label="Tag name" value={name} onChange={e => { setName(e.target.value); setError(null) }} placeholder="New tag, for example production" maxLength={40} aria-invalid={!!error} />
            {error && <p role="alert" className="mt-1.5 text-[12px] text-danger">{error}</p>}
          </div>
          <Button type="submit" variant="primary" busy={busy} icon={<Icon.Plus className="h-3.5 w-3.5" />}>Add tag</Button>
        </div>
        <Swatches value={color} onChange={setColor} label="Colour of the new tag" />
      </form>

      {tags.length === 0
        ? <p className="text-[13px] text-muted">No tags yet. Tags label monitors so you can find and group them.</p>
        : (
          <ul aria-label="Tags" className="divide-y divide-default overflow-hidden rounded-[12px] border border-default">
            {tags.map(t => (
              <li key={t.id} className="px-4 py-3">
                {editing === t.id ? (
                  <form className="flex flex-col gap-3" onSubmit={e => { e.preventDefault(); void saveEdit(t) }}>
                    <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
                      <div className="min-w-0 flex-1">
                        <Input aria-label={`Name of ${t.name}`} value={editName} onChange={e => { setEditName(e.target.value); setEditError(null) }} maxLength={40} autoFocus aria-invalid={!!editError} />
                        {editError && <p role="alert" className="mt-1.5 text-[12px] text-danger">{editError}</p>}
                      </div>
                      <span className="flex items-center gap-2">
                        <Button type="submit" variant="primary" busy={busy}>Save</Button>
                        <Button variant="ghost" onClick={() => setEditing(null)} disabled={busy}>Cancel</Button>
                      </span>
                    </div>
                    <Swatches value={editColor} onChange={setEditColor} label={`Colour of ${t.name}`} />
                  </form>
                ) : (
                  <div className="flex items-center gap-3">
                    <span aria-hidden="true" className="h-3 w-3 shrink-0 rounded-full" style={{ backgroundColor: dot(t.color) }} />
                    <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-body">{t.name}</span>
                    <span className="shrink-0 text-[12px] tnum text-muted">{usage[t.id] ?? 0} {(usage[t.id] ?? 0) === 1 ? 'monitor' : 'monitors'}</span>
                    <Button variant="ghost" size="sm" onClick={() => startEdit(t)} aria-label={`Edit tag ${t.name}`} icon={<Icon.Edit className="h-3.5 w-3.5" />}>Edit</Button>
                    <button type="button" aria-label={`Delete tag ${t.name}`} onClick={() => setDeleting(t)}
                      className="rounded-lg p-1.5 text-muted transition-colors hover:bg-surface-hover hover:text-danger focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40">
                      <Icon.Trash className="h-3.5 w-3.5" />
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

      <ConfirmModal
        open={deleting !== null}
        title={`Delete tag "${deleting?.name ?? ''}"?`}
        description={inUse > 0 ? `It is removed from the ${inUse} ${inUse === 1 ? 'monitor' : 'monitors'} that use it. The monitors themselves are not changed.` : 'No monitors use this tag.'}
        confirmLabel="Delete tag"
        onClose={() => setDeleting(null)}
        onConfirm={async () => { if (deleting) await remove(deleting); setDeleting(null) }}
      />
    </div>
  )
}
