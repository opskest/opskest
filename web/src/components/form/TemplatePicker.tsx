// "Starting configuration": the workspace's own saved templates. Every
// service has different needs, so there are no built-in presets — configure a
// monitor once, save it, and start the next one from it. A template keeps the
// check's settings, not its name, target, project or secrets.

import { useState } from 'react'
import { api, errorText } from '../../lib/api'
import { MONITOR_TYPES, type Draft } from '../../lib/monitorForm'
import { templateConfig, type MonitorTemplate } from '../../lib/templates'
import { Button, Icon, Input, Modal, useToast } from '../ui'
import { Collapsible } from '../controls'
import { Field, FieldGrid } from './primitives'

interface Props {
  draft: Draft
  templates: MonitorTemplate[] | null
  /** Create mode only: apply a template (null = start from scratch). */
  onApply?: (t: MonitorTemplate | null) => void
  selectedId: number | null
  onChange: (next: MonitorTemplate[]) => void
  loadError?: boolean
}

function summary(t: MonitorTemplate): string {
  const c = t.config
  const parts: string[] = []
  if (c.intervalS) parts.push(`every ${c.intervalS >= 60 && c.intervalS % 60 === 0 ? `${c.intervalS / 60} min` : `${c.intervalS} s`}`)
  if (c.retries != null) parts.push(c.retries === 0 ? 'down on first failure' : `down after ${c.retries + 1} failures`)
  if (c.notifyDegraded) parts.push('alerts when degraded')
  if (c.reminderMin) parts.push(`reminds every ${c.reminderMin} min`)
  return parts.join(' · ')
}

export function TemplatePicker({ draft, templates, onApply, selectedId, onChange, loadError }: Props) {
  const { push } = useToast()
  const [saving, setSaving] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null)
  const list = templates ?? []
  const create = onApply != null

  async function save() {
    const n = name.trim()
    if (!n) { setError('Give the template a name.'); return }
    setBusy(true)
    setError(null)
    try {
      const made = await api.post<MonitorTemplate>('/api/templates', { name: n, description: description.trim(), config: templateConfig(draft) })
      onChange([...list, made].sort((a, b) => a.name.localeCompare(b.name)))
      push(`Saved template "${n}".`, 'ok')
      setSaving(false)
      setName('')
      setDescription('')
    } catch (err) {
      setError(errorText(err, 'Could not save the template.'))
    } finally {
      setBusy(false)
    }
  }

  async function remove(t: MonitorTemplate) {
    setBusy(true)
    try {
      await api.del(`/api/templates/${t.id}`)
      onChange(list.filter(x => x.id !== t.id))
      push(`Deleted template "${t.name}".`, 'ok')
    } catch (err) {
      push(errorText(err, 'Could not delete the template.'), 'danger')
    } finally {
      setBusy(false)
      setConfirmDelete(null)
    }
  }

  const chosen = list.find(t => t.id === selectedId)
  const summaryText = create
    ? (chosen ? `Using "${chosen.name}"` : list.length > 0 ? `${list.length} saved · optional` : 'Optional')
    : (list.length > 0 ? `${list.length} saved` : 'None saved yet')

  return (
    <>
    <Collapsible
      title={<span className="inline-flex items-center gap-2"><Icon.Bolt className="h-3.5 w-3.5 text-muted" />Templates</span>}
      summary={summaryText}
    >
      <div className="flex flex-col gap-4">
      <p className="text-[12px] text-muted">{create ? 'Start from a saved configuration, or set everything yourself.' : "Save this monitor's settings to start future monitors from them."}</p>
      {loadError && <p className="text-[12px] text-danger">Couldn&apos;t load saved templates.</p>}

      {create && (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <button type="button" aria-pressed={selectedId === null} onClick={() => onApply?.(null)}
            className={`text-left rounded-lg border px-3.5 py-3 transition ${selectedId === null ? 'border-accent bg-accent/[0.06] ring-1 ring-accent' : 'border-default bg-surface hover:border-strong'}`}>
            <span className="text-[13px] font-semibold text-body">Blank</span>
            <span className="mt-1.5 block text-[11px] text-muted leading-snug">Set everything yourself. Anything already filled in stays.</span>
          </button>
          {list.map(t => (
            <div key={t.id} className={`relative rounded-lg border transition ${selectedId === t.id ? 'border-accent bg-accent/[0.06] ring-1 ring-accent' : 'border-default bg-surface hover:border-strong'}`}>
              <button type="button" aria-pressed={selectedId === t.id} onClick={() => onApply?.(t)} className="block w-full text-left px-3.5 py-3 pr-10">
                <span className="flex items-center gap-2">
                  <span className="text-[13px] font-semibold text-body truncate">{t.name}</span>
                  {selectedId === t.id && <Icon.Check className="w-4 h-4 text-accent shrink-0" />}
                </span>
                <span className="mt-1 block text-[11px] uppercase tracking-wide text-muted">{MONITOR_TYPES.find(m => m.type === t.type)?.label ?? t.type}</span>
                {t.description && <span className="mt-1.5 block text-[12px] text-body-2 leading-snug">{t.description}</span>}
                <span className="mt-1.5 block text-[11px] text-muted leading-snug">{summary(t)}</span>
              </button>
              {confirmDelete === t.id
                ? (
                  <span className="absolute right-2 top-2 flex items-center gap-1">
                    <Button variant="danger" onClick={() => remove(t)} busy={busy}>Delete</Button>
                    <Button variant="ghost" onClick={() => setConfirmDelete(null)}>Keep</Button>
                  </span>
                )
                : (
                  <button type="button" aria-label={`Delete template ${t.name}`} onClick={() => setConfirmDelete(t.id)}
                    className="absolute right-2 top-2 p-1.5 rounded-lg text-muted hover:text-danger hover:bg-surface-hover">
                    <Icon.Trash className="w-3.5 h-3.5" />
                  </button>
                )}
            </div>
          ))}
        </div>
      )}

      {create && templates && list.length === 0 && !loadError && (
        <p className="text-[12px] text-muted leading-relaxed">
          No templates yet. Configure a monitor below, then save its settings as a template to reuse them.
        </p>
      )}

      {!create && list.length > 0 && (
        <ul className="flex flex-wrap gap-2" aria-label="Saved templates">
          {list.map(t => <li key={t.id} className="rounded-full border border-default px-2.5 py-1 text-[12px] text-body-2">{t.name}</li>)}
        </ul>
      )}

      <div>
        <Button variant="secondary" onClick={() => setSaving(true)} icon={<Icon.Plus className="w-3.5 h-3.5" />}>
          Save these settings as a template
        </Button>
      </div>
      </div>
    </Collapsible>

      <Modal
        open={saving}
        onClose={() => { if (!busy) { setSaving(false); setError(null) } }}
        title="Save as template"
        description="Saves the type, schedule, alert and check settings. The name, target, project, passwords and tokens are not saved."
        size="lg"
        footer={<>
          <Button variant="ghost" onClick={() => { setSaving(false); setError(null) }} disabled={busy}>Cancel</Button>
          <Button variant="primary" onClick={save} busy={busy}>Save template</Button>
        </>}
      >
        <FieldGrid>
          <Field label="Template name" error={error ?? undefined}>
            {id => <Input id={id} value={name} onChange={e => setName(e.target.value)} placeholder="Production API" maxLength={80} autoFocus
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void save() } }} />}
          </Field>
          <Field label="Description" optional>
            {id => <Input id={id} value={description} onChange={e => setDescription(e.target.value)} placeholder="What it is for" maxLength={300} />}
          </Field>
        </FieldGrid>
      </Modal>
    </>
  )
}
