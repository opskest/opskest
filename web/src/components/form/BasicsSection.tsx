// The monitor form's type picker and General section: name, target (label,
// placeholder and inline validation per type), group (with "New group"
// inline), tags and description.

import { useMemo, useState, type KeyboardEvent, type ReactNode } from 'react'
import { api, errorText, unwrapList } from '../../lib/api'
import { isStatusPageTarget, MONITOR_TYPES, NO_TARGET_TYPES, TARGET, TYPE_GROUPS, type Draft } from '../../lib/monitorForm'
import { useLive } from '../../lib/live'
import { normGroup } from '../../lib/normalize'
import type { FieldErrors, Group, MonitorType, Tag } from '../../lib/types'
import { TagManager } from '../TagManager'
import { Button, Icon, Input, Modal, Select, Textarea } from '../ui'
import { Field, FieldGrid, FieldMessage, Section } from './primitives'

const TYPE_ICON: Record<MonitorType, (p: { className?: string }) => ReactNode> = {
  http: Icon.Globe,
  tcp: Icon.Plug,
  ping: Icon.Activity,
  dns: Icon.Server,
  push: Icon.Inbox,
  websocket: Icon.Arrows,
  vendor: Icon.Rss,
  tls: Icon.Lock,
  smtp: Icon.Inbox,
  ssh: Icon.System,
  redis: Icon.Bolt,
  postgres: Icon.Database,
  mysql: Icon.Database,
  mongodb: Icon.Database,
  mqtt: Icon.Arrows,
  ntp: Icon.Clock,
  udp: Icon.Plug,
  amqp: Icon.Arrows,
  memcached: Icon.Database,
  grpc: Icon.Activity,
  banner: Icon.List,
  postgres_sql: Icon.Database,
  mysql_sql: Icon.Database,
  group: Icon.Grid,
  manual: Icon.Wrench,
}

export function TypeIcon({ type, className }: { type: MonitorType; className?: string }) {
  const I = TYPE_ICON[type] ?? Icon.Globe
  return <I className={className} />
}

/** The type cards, grouped (Web, Network, Services, Heartbeat); arrow keys move between them. */
export function TypePicker({ value, locked, onChange }: { value: MonitorType; locked: boolean; onChange: (t: MonitorType) => void }) {
  const types = MONITOR_TYPES.some(t => t.type === value)
    ? MONITOR_TYPES
    : [...MONITOR_TYPES, { type: value, label: '3rd-party status', desc: 'A vendor status feed', group: 'Web' as const }]
  // Keyboard order follows what is on screen: group by group.
  const ordered = TYPE_GROUPS.flatMap(g => types.filter(t => t.group === g))

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (locked) return
    const i = ordered.findIndex(t => t.type === value)
    const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
    if (!step) return
    e.preventDefault()
    const next = (i + step + ordered.length) % ordered.length
    onChange(ordered[next].type)
    e.currentTarget.querySelector<HTMLButtonElement>(`[data-type="${ordered[next].type}"]`)?.focus()
  }

  return (
    <div role="radiogroup" aria-label="Monitor type" onKeyDown={onKeyDown} className="flex flex-col gap-5">
      {TYPE_GROUPS.map(group => {
        const items = types.filter(t => t.group === group)
        if (items.length === 0) return null
        return (
          <div key={group} className="flex flex-col gap-2">
            <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted">{group}</h3>
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {items.map(t => {
                const selected = t.type === value
                return (
                  <button
                    key={t.type}
                    type="button"
                    role="radio"
                    data-type={t.type}
                    aria-checked={selected}
                    tabIndex={selected ? 0 : -1}
                    disabled={locked && !selected}
                    onClick={() => onChange(t.type)}
                    className={`group relative text-left rounded-md border px-3 py-3 flex items-start gap-3 transition-colors
                      focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-45 disabled:cursor-not-allowed
                      ${selected ? 'border-accent-border bg-accent-subtle' : 'border-default hover:bg-surface-hover/50'}`}
                  >
                    <span className={`w-6 h-6 flex items-center justify-center shrink-0 mt-0.5 transition-colors ${selected ? 'text-accent' : 'text-muted group-hover:text-body'}`}>
                      <TypeIcon type={t.type} className="w-4 h-4" />
                    </span>
                    <span className="flex flex-col gap-0.5 min-w-0">
                      <span className={`text-[13px] font-semibold ${selected ? 'text-accent' : 'text-body'}`}>{t.label}</span>
                      <span className="text-[12px] text-muted leading-snug">{t.desc}</span>
                    </span>
                  </button>
                )
              })}
            </div>
          </div>
        )
      })}
    </div>
  )
}

/** The inline status-page notice under an HTTP target that looks like a vendor status page or feed. */
export function StatusPageNote() {
  return (
    <div role="note" className="flex items-start gap-2.5 rounded-lg border border-accent-border bg-accent-subtle px-3.5 py-2.5 text-[12px] text-body-2 leading-relaxed">
      <Icon.Rss className="w-4 h-4 mt-0.5 text-accent shrink-0" />
      <p>
        <span className="font-semibold text-body">Looks like a 3rd-party status page.</span>{' '}
        It&apos;s read for incidents automatically (Atlassian Statuspage, Google, Slack, Meta, RSS/Atom): the monitor goes
        degraded or down only when the vendor reports an incident, and the alert carries the incident title. Response
        time never degrades it.
      </p>
    </div>
  )
}

function toggleIn(list: number[], v: number): number[] {
  return list.includes(v) ? list.filter(x => x !== v) : [...list, v]
}

export function GeneralSection({
  draft: d, errors, groups, tags, onPatch, onGroupCreated, onTagsChange, targetError, onTargetBlur,
}: {
  draft: Draft
  errors: FieldErrors
  groups: Group[]
  tags: Tag[]
  onPatch: (p: Partial<Draft>) => void
  onGroupCreated: (g: Group) => void
  /** Tags were created, renamed or deleted in the manager dialog. */
  onTagsChange?: (tags: Tag[]) => void
  /** Inline (as-you-type) target validation, shown once the field was left. */
  targetError?: string
  onTargetBlur?: () => void
}) {
  const [open, setOpen] = useState(false)
  const [managingTags, setManagingTags] = useState(false)
  const live = useLive()
  const tagUsage = useMemo(() => {
    const counts: Record<number, number> = {}
    for (const m of live.monitors.values()) for (const id of m.tagIds ?? []) counts[id] = (counts[id] ?? 0) + 1
    return counts
  }, [live.monitors])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const type = d.type
  const tErr = errors.target ?? targetError

  async function createGroup() {
    const n = name.trim()
    if (!n) { setError('Enter a name.'); return }
    setBusy(true)
    setError(null)
    try {
      const res = await api.post<unknown>('/api/groups', { name: n, description: '' })
      let g: Group | undefined = normGroup(res)
      if (!g.id) g = unwrapList(await api.get<unknown>('/api/groups'), 'groups').map(normGroup).find(x => x.name === n)
      if (g) {
        onGroupCreated(g)
        onPatch({ groupId: g.id })
      }
      setOpen(false)
      setName('')
    } catch (err) {
      setError(errorText(err, 'Could not create the project.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section title="Service details" hint="Name the service and choose where it belongs." icon={<Icon.Edit className="w-3.5 h-3.5" />}>
      <FieldGrid>
      <Field label="Name" error={errors.name}>
        {fid => <Input id={fid} value={d.name} onChange={e => onPatch({ name: e.target.value })} placeholder="Checkout API" maxLength={120} aria-invalid={!!errors.name} />}
      </Field>

      {!NO_TARGET_TYPES.includes(type)
        ? (
          <div className="min-w-0">
            <Field label={TARGET[type as keyof typeof TARGET].label} error={tErr} hint={TARGET_HINT[type as keyof typeof TARGET_HINT]}>
              {fid => (
                <Input
                  id={fid}
                  value={d.target}
                  onChange={e => onPatch({ target: e.target.value })}
                  onBlur={onTargetBlur}
                  placeholder={TARGET[type as keyof typeof TARGET].placeholder}
                  className="font-mono"
                  aria-invalid={!!tErr}
                  spellCheck={false}
                  autoCapitalize="off"
                  autoComplete="off"
                  inputMode={type === 'http' || type === 'websocket' || type === 'vendor' ? 'url' : 'text'}
                />
              )}
            </Field>
          </div>
        )
        : (
          <div className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed sm:mt-[26px]">
            <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
            <p>{NO_TARGET_NOTE[type] ?? NO_TARGET_NOTE.push}</p>
          </div>
        )}
      </FieldGrid>
      {type === 'http' && !NO_TARGET_TYPES.includes(type) && !tErr && isStatusPageTarget(d.target) && <StatusPageNote />}

      <FieldGrid>
        <Field label="Project" error={errors.groupId}>
          {fid => (
            <div className="flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <Select
                  id={fid}
                  value={d.groupId == null ? '' : String(d.groupId)}
                  onChange={e => onPatch({ groupId: e.target.value === '' ? null : Number(e.target.value) })}
                  className="flex-1 min-w-0"
                >
                  <option value="">Unassigned</option>
                  {groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
                  {d.groupId != null && !groups.some(g => g.id === d.groupId) && <option value={d.groupId}>Project {d.groupId}</option>}
                </Select>
                {!open && (
                  <Button variant="secondary" onClick={() => { setOpen(true); setError(null) }} icon={<Icon.Plus className="w-3.5 h-3.5" />} aria-label="New project">
                    <span className="hidden min-[440px]:inline">New</span>
                  </Button>
                )}
              </div>
              {open && (
                <div className="flex flex-col gap-1.5 rounded-md border border-default bg-surface-2/50 p-2.5">
                  <div className="flex items-center gap-2">
                    <Input
                      aria-label="New project name"
                      placeholder="Project name"
                      value={name}
                      onChange={e => setName(e.target.value)}
                      onKeyDown={e => {
                        if (e.key === 'Enter') { e.preventDefault(); void createGroup() }
                        if (e.key === 'Escape') { e.preventDefault(); setOpen(false) }
                      }}
                      autoFocus
                      className="flex-1 min-w-0"
                      maxLength={100}
                    />
                    <Button size="sm" variant="primary" busy={busy} onClick={() => void createGroup()}>Create</Button>
                    <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>Cancel</Button>
                  </div>
                  {error && <FieldMessage error={error} />}
                </div>
              )}
            </div>
          )}
        </Field>

        <div className="flex flex-col gap-1.5 min-w-0">
          <span className="text-[12px] font-medium text-body-2" id="tags-label">Tags</span>
          <div role="group" aria-labelledby="tags-label" className="flex flex-wrap gap-1.5 min-h-9 items-center">
            {tags.length === 0 && <span className="text-[12px] text-muted">No tags yet.</span>}
            {tags.map(t => {
              const on = d.tagIds.includes(t.id)
              return (
                <button
                  key={t.id}
                  type="button"
                  aria-pressed={on}
                  onClick={() => onPatch({ tagIds: toggleIn(d.tagIds, t.id) })}
                  className={`inline-flex items-center gap-1.5 h-7 px-2.5 rounded-full border text-[12px] font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40
                    ${on ? 'border-accent bg-accent-subtle text-body' : 'border-default bg-surface-2 text-muted hover:border-strong hover:text-body'}`}
                >
                  {on
                    ? <Icon.Check className="w-3 h-3 text-accent" />
                    : <span aria-hidden="true" className="w-2 h-2 rounded-full bg-faint" style={/^#[0-9a-f]{3,6}$/i.test(t.color) ? { backgroundColor: t.color } : undefined} />}
                  {t.name}
                </button>
              )
            })}
            {onTagsChange && (
              <button type="button" onClick={() => setManagingTags(true)}
                className="inline-flex h-7 items-center gap-1.5 rounded-full border border-dashed border-default px-2.5 text-[12px] font-medium text-muted transition-colors hover:border-strong hover:text-body focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40">
                <Icon.Plus className="h-3 w-3" />{tags.length === 0 ? 'Create a tag' : 'Manage tags'}
              </button>
            )}
          </div>
          <FieldMessage error={errors.tagIds} />
        </div>
      </FieldGrid>

      <Field label="Description" optional error={errors.description} hint="What this checks and why it matters — shown on the monitor's page.">
        {fid => <Textarea id={fid} rows={2} value={d.description} onChange={e => onPatch({ description: e.target.value })} aria-invalid={!!errors.description} />}
      </Field>

      {onTagsChange && (
        <Modal open={managingTags} onClose={() => setManagingTags(false)} title="Manage tags" description="Create, rename, recolour or delete tags. A tag you create here is added to this monitor."
          footer={<Button variant="primary" onClick={() => setManagingTags(false)}>Done</Button>}>
          <TagManager tags={tags} usage={tagUsage} onChange={onTagsChange}
            onCreated={t => onPatch({ tagIds: d.tagIds.includes(t.id) ? d.tagIds : [...d.tagIds, t.id] })}
            onDeleted={id => onPatch({ tagIds: d.tagIds.filter(x => x !== id) })} />
        </Modal>
      )}
    </Section>
  )
}

const NO_TARGET_NOTE: Record<string, string> = {
  push: 'A push monitor has no target — it gets its own URL, and your job calls it every interval.',
  manual: 'A manual monitor has no target — you set it up, degraded or down from its page (or the API), for work no check can see, like a nightly job or a planned change.',
  group: 'A group has no target — it follows the monitors you pick below and shows as one status.',
}

const TARGET_HINT: Record<Exclude<MonitorType, 'push' | 'group' | 'manual'>, string> = {
  http: 'The full URL, including http:// or https://.',
  tcp: 'Host (or IP) and port, e.g. db.internal:5432 or [::1]:6379.',
  ping: 'A hostname or an IPv4/IPv6 address.',
  dns: 'The name to resolve — no scheme or path.',
  websocket: 'A ws:// or wss:// URL.',
  tls: 'A hostname, optionally with a port (443 by default). No scheme.',
  smtp: 'Mail server and port, e.g. mail.example.com:587.',
  ssh: 'Host and port, usually :22.',
  redis: 'Host and port, usually :6379.',
  postgres: 'Host and port, usually :5432.',
  mysql: 'Host and port, usually :3306.',
  mongodb: 'Host and port, usually :27017.',
  mqtt: 'Broker host and port, usually :1883 (or :8883 with TLS).',
  ntp: 'A time server, optionally with a port (123 by default).',
  udp: 'Host and port of the UDP service.',
  amqp: 'Broker host and port, usually :5672 (or :5671 with TLS).',
  memcached: 'Host and port, usually :11211.',
  grpc: 'Host and port of the gRPC server. It must expose the standard health service.',
  postgres_sql: 'Host and port of the PostgreSQL server, usually :5432.',
  mysql_sql: 'Host and port of the MySQL or MariaDB server, usually :3306.',
  banner: 'Host and port of a service that greets on connect (FTP :21, IMAP :143, POP3 :110).',
  vendor: 'The vendor status page or its incident feed.',
}
