import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ApiError, api, errorText } from '../../lib/api'
import { ago, dateServer, duration } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import type { FieldErrors } from '../../lib/types'
import {
  INSTALL_STEPS, bareVersion, formatBytes, isActivePhase, isDevVersion, linkText, normalizePayload, parseNotes, phaseLabel, pollAction,
  stepStates, validFeedUrl,
  type Block, type Inline, type InstallMethod, type PollResult, type UpdateMode, type UpdateSettingsIn, type UpdatesPayload,
} from '../../lib/updates'
import { Field, FieldGrid, FieldMessage } from '../form/primitives'
import { Alert, Badge, Button, ConfirmModal, ErrorRetry, Icon, Input, Select, Spinner, Switch, Textarea, useToast } from '../ui'
import { Collapsible } from '../controls'
import { CopyField, SettingsFooter, SettingsLoading, SettingsSection } from './SettingsPanel'

// Settings -> Updates. Shows what is running and what is available, lets an
// administrator check, install or roll back, and edits the update source and
// schedule. An install restarts the server, so after accepting one we poll
// GET /api/updates, treat failed requests as expected while the process is
// away (retrying every 1.5 s for up to 90 s), and reload once the new version
// answers.

const POLL_MS = 1500
// Set just before the page reloads into a new version, so the toast survives the reload.
const UPDATED_KEY = 'opskest-updated'
const DAY_MS = 86_400_000

export const METHOD_TEXT: Record<InstallMethod, { label: string; meaning: string }> = {
  binary: { label: 'Program file', meaning: 'A single program file that Opskest can replace and restart by itself.' },
  docker: { label: 'Docker container', meaning: 'Runs from a container image, so updates come from pulling a newer image.' },
  other: { label: 'Other', meaning: 'Installed in a way Opskest cannot always update by itself.' },
}

interface Wait { kind: 'install' | 'rollback' | 'watch'; startVersion: string; startedAt: number }
type Outcome = 'stalled' | 'timeout' | 'reloading' | null
type KeyMode = 'keep' | 'replace' | 'clear'

const HOURS = Array.from({ length: 24 }, (_, h) => h)
const hh = (h: number) => `${String(h).padStart(2, '0')}:00`

const MODE_TEXT: Record<UpdateMode, string> = { off: 'Never check', notify: 'Notify me', auto: 'Install automatically' }

function feedHost(url: string): string {
  try { return new URL(url).host } catch { return '' }
}

/** "v1.4.0" for any spelling of a version, "" when there is none. */
const vTag = (version: string) => (version ? `v${bareVersion(version)}` : '')

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

function SoftNote({ children }: { children: ReactNode }) {
  return <p className="text-[12px] leading-snug text-muted [overflow-wrap:anywhere]">{children}</p>
}

/** The install steps, the current one highlighted; the download step carries the percent bar. */
function StepList({ phase, percent, steps }: { phase: string; percent: number; steps: typeof INSTALL_STEPS }) {
  const all = stepStates(phase)
  const pct = Math.max(0, Math.min(100, Math.round(percent)))
  return (
    <ol aria-label="Update steps" className="space-y-2.5">
      {steps.map(step => {
        const st = all[INSTALL_STEPS.indexOf(step)]
        const showBar = st === 'current' && step.phase === 'downloading'
        return (
          <li key={step.phase} aria-current={st === 'current' ? 'step' : undefined} className="flex items-start gap-2.5">
            <span aria-hidden="true" className={`mt-px grid h-5 w-5 shrink-0 place-items-center rounded-full ${
              st === 'done' ? 'bg-ok-bg text-ok' : st === 'current' ? 'bg-accent-subtle text-accent' : 'border border-default text-faint'}`}>
              {st === 'done' ? <Icon.Check className="h-3 w-3" /> : st === 'current' ? <Spinner /> : null}
            </span>
            <div className="min-w-0 flex-1">
              <div className={`text-[13px] leading-5 ${st === 'current' ? 'font-medium text-body' : st === 'done' ? 'text-muted' : 'text-faint'}`}>
                {step.label}
                <span className="sr-only">{st === 'done' ? ', done' : st === 'current' ? ', in progress' : ', waiting'}</span>
                {showBar && percent > 0 && <span className="tnum text-muted"> {pct}%</span>}
              </div>
              {showBar && (
                <div
                  role="progressbar" aria-label="Download progress" aria-valuemin={0} aria-valuemax={100}
                  aria-valuenow={percent > 0 ? pct : undefined}
                  className="mt-1.5 h-2 overflow-hidden rounded-full bg-surface-2"
                >
                  <div
                    className={`h-full rounded-full bg-accent transition-[width] duration-300 ${percent > 0 ? '' : 'w-full animate-pulse opacity-60 motion-reduce:animate-none'}`}
                    style={percent > 0 ? { width: `${pct}%` } : undefined}
                  />
                </div>
              )}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

function InlineText({ items }: { items: Inline[] }) {
  return (
    <>
      {items.map((it, i) => {
        if (it.kind === 'bold') return <strong key={i} className="font-semibold text-body">{it.text}</strong>
        if (it.kind === 'code') return <code key={i} className="font-mono text-[12px] bg-surface-2 border border-default rounded-md px-1 py-px">{it.text}</code>
        // Links are shown as text, never as anchors: the notes come from a remote feed.
        if (it.kind === 'link') return <span key={i} title={linkText(it)}>{it.text !== it.url && <>{it.text} </>}<span className="font-mono text-[12px] break-all">{it.text === it.url ? it.url : `(${it.url})`}</span></span>
        return <span key={i}>{it.text}</span>
      })}
    </>
  )
}

/** Rough number of text lines the notes take, to decide whether to clamp them. */
function noteLines(blocks: Block[]): number {
  return blocks.reduce((n, b) => n + (b.kind === 'list' ? b.items.length : b.kind === 'code' ? b.text.split(/\r?\n/).length : 1), 0)
}

function NotesView({ blocks, clamp = false }: { blocks: Block[]; clamp?: boolean }) {
  if (blocks.length === 0) return <p className="text-[13px] text-muted">No release notes were published for this version.</p>
  return (
    <div className={`${clamp ? 'max-h-44 overflow-hidden' : 'max-h-80 overflow-y-auto scroll-slim'} space-y-3 text-[13px] text-body-2 leading-relaxed [overflow-wrap:anywhere]`}>
      {blocks.map((b, i) => {
        if (b.kind === 'heading') return <h4 key={i} className="text-sm font-semibold text-body pt-1"><InlineText items={b.inlines} /></h4>
        if (b.kind === 'paragraph') return <p key={i}><InlineText items={b.inlines} /></p>
        if (b.kind === 'code') return <pre key={i} className="font-mono text-[12px] bg-surface-2 border border-default rounded-lg p-3 overflow-x-auto whitespace-pre">{b.text}</pre>
        const List = b.ordered ? 'ol' : 'ul'
        return (
          <List key={i} className={`pl-5 space-y-1 ${b.ordered ? 'list-decimal' : 'list-disc'}`}>
            {b.items.map((item, j) => <li key={j}><InlineText items={item} /></li>)}
          </List>
        )
      })}
    </div>
  )
}

/** The newest release's notes, open by default, about eight lines until "Show all". */
function WhatsNew({ meta, blocks }: { meta: string; blocks: Block[] }) {
  const [all, setAll] = useState(false)
  const long = noteLines(blocks) > 8
  return (
    <Collapsible title="What's new" defaultOpen>
      <div className="space-y-2">
        {meta && <SoftNote>{meta}</SoftNote>}
        <NotesView blocks={blocks} clamp={long && !all} />
        {long && (
          <button type="button" className="text-[12px] font-medium text-accent hover:underline underline-offset-2" onClick={() => setAll(a => !a)}>
            {all ? 'Show less' : 'Show all'}
          </button>
        )}
      </div>
    </Collapsible>
  )
}

/** The source or key is missing or invalid, or the last check failed: the settings need a look. */
function needsSetup(d: UpdatesPayload): boolean {
  if (isDevVersion(d.current)) return false
  const s = d.settings
  return !s.feedUrl || !validFeedUrl(s.feedUrl) || !s.publicKeySet || !!d.check.error
}

export function UpdatesPanel({ onData, scrollHere = false }: { onData?: (p: UpdatesPayload) => void; scrollHere?: boolean }) {
  const toast = useToast()
  const [data, setData] = useState<UpdatesPayload | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)
  const [confirm, setConfirm] = useState<'install' | 'rollback' | null>(null)
  const [wait, setWait] = useState<Wait | null>(null)
  const [outcome, setOutcome] = useState<Outcome>(null)
  const [unreachable, setUnreachable] = useState(false)
  const [, setClock] = useState(0)
  const rt = useRef<{ sawActive: boolean; lostAt: number | null }>({ sawActive: false, lostAt: null })

  // Settings form
  const [enabled, setEnabled] = useState(false)
  const [feedUrl, setFeedUrl] = useState('')
  const [mode, setMode] = useState<UpdateMode>('notify')
  const [autoHour, setAutoHour] = useState(3)
  const [keyMode, setKeyMode] = useState<KeyMode>('replace')
  const [keyValue, setKeyValue] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [saving, setSaving] = useState(false)
  // null until the first load decides; then the person's own choice.
  const [settingsOpen, setSettingsOpen] = useState<boolean | null>(null)

  // A key entered in settings can be replaced or removed here; one built into the binary can only be overridden.
  const savedKey = !!data && data.settings.publicKeySet && data.settings.publicKeySource !== 'build'

  const syncForm = useCallback((s: UpdatesPayload['settings']) => {
    setEnabled(s.enabled); setFeedUrl(s.feedUrl); setMode(s.mode); setAutoHour(s.autoHour)
    setKeyMode(s.publicKeySet && s.publicKeySource !== 'build' ? 'keep' : 'replace'); setKeyValue(''); setErrors({})
  }, [])

  // Relative times ("in 3h 12m") stay current without another request.
  useEffect(() => {
    const t = window.setInterval(() => setClock(c => c + 1), 30_000)
    return () => window.clearInterval(t)
  }, [])

  // Right after an update the page reloads; say what happened once the new version is up.
  useEffect(() => {
    try {
      const msg = window.sessionStorage.getItem(UPDATED_KEY)
      if (msg) { window.sessionStorage.removeItem(UPDATED_KEY); toast.push(msg, 'ok') }
    } catch { /* storage may be unavailable */ }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const startWait = useCallback((kind: Wait['kind'], startVersion: string) => {
    rt.current = { sawActive: kind === 'watch', lostAt: null }
    setUnreachable(false); setOutcome(null)
    setWait({ kind, startVersion, startedAt: Date.now() })
  }, [])

  const load = useCallback(() => {
    setError(null)
    api.get<UpdatesPayload>('/api/updates')
      .then(p => {
        if (!p || typeof p !== 'object' || typeof p.current !== 'string') throw new Error('The server did not return update information.')
        const n = normalizePayload(p)
        setData(n); syncForm(n.settings)
        setSettingsOpen(o => o ?? needsSetup(n))
        // An update may already be running (started elsewhere, or before a page reload).
        if (isActivePhase(n.state.phase)) setWait(w => { if (w) return w; rt.current = { sawActive: true, lostAt: null }; return { kind: 'watch', startVersion: n.current, startedAt: Date.now() } })
      })
      .catch(e => setError(errorText(e, 'Could not load update information')))
  }, [syncForm])
  useEffect(load, [load])
  // The About tab reads the install method and version from here instead of asking twice.
  useEffect(() => { if (data) onData?.(data) }, [data, onData])
  // ?tab=updates and #updates land on this section.
  const scrolled = useRef(false)
  useEffect(() => {
    if (!scrollHere || !data || scrolled.current) return
    scrolled.current = true
    window.setTimeout(() => document.getElementById('updates')?.scrollIntoView({ block: 'start' }), 60)
  }, [scrollHere, data])

  // Poll while an update or rollback is in flight.
  useEffect(() => {
    if (!wait) return
    let alive = true
    let timer = 0
    const tick = async () => {
      const now = Date.now()
      let poll: PollResult
      try {
        const p = await api.get<UpdatesPayload>('/api/updates')
        if (!p || typeof p.current !== 'string') throw new Error('unexpected body')
        const n = normalizePayload(p)
        if (!alive) return
        setData(n)
        if (isActivePhase(n.state.phase)) rt.current.sawActive = true
        rt.current.lostAt = null
        setUnreachable(false)
        poll = { ok: true, current: n.current, phase: n.state.phase }
      } catch {
        if (!alive) return
        // A server that is restarting does not answer; that is expected.
        rt.current.sawActive = true
        if (rt.current.lostAt == null) rt.current.lostAt = now
        setUnreachable(true)
        poll = { ok: false }
      }
      const action = pollAction({ startVersion: wait.startVersion, startedAt: wait.startedAt, now, ...rt.current }, poll)
      if (action === 'continue') { timer = window.setTimeout(tick, POLL_MS); return }
      if (action === 'reload') {
        // Reload so the page runs the new version's own scripts; the toast follows the reload.
        const running = poll.ok ? poll.current : ''
        const msg = wait.kind === 'rollback' ? `Rolled back to ${vTag(running)}` : `Updated to ${vTag(running)}`
        let stored = false
        try { window.sessionStorage.setItem(UPDATED_KEY, msg); stored = true } catch { /* storage may be unavailable */ }
        if (!stored) toast.push(msg, 'ok')
        setOutcome('reloading')
        window.setTimeout(() => window.location.reload(), stored ? 600 : 1800)
        return
      }
      setWait(null)
      if (action === 'failed') toast.push('The update did not complete', 'danger')
      else if (action === 'timeout') setOutcome('timeout')
      else if (wait.kind !== 'watch') setOutcome('stalled')
    }
    timer = window.setTimeout(tick, POLL_MS)
    return () => { alive = false; window.clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wait])

  const dirty = data != null && (
    enabled !== data.settings.enabled || feedUrl.trim() !== data.settings.feedUrl || mode !== data.settings.mode ||
    autoHour !== data.settings.autoHour || keyMode === 'clear' || (keyMode === 'replace' && keyValue.trim() !== '')
  )
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  // Reads the current state without touching the settings form (used when a request says an operation is running).
  async function peek() {
    try {
      const p = await api.get<UpdatesPayload>('/api/updates')
      if (!p || typeof p.current !== 'string') return
      const n = normalizePayload(p)
      setData(n)
      if (isActivePhase(n.state.phase)) setWait(w => { if (w) return w; rt.current = { sawActive: true, lostAt: null }; return { kind: 'watch', startVersion: n.current, startedAt: Date.now() } })
    } catch { /* the next poll or a reload shows it */ }
  }

  async function checkNow() {
    if (checking) return
    setChecking(true)
    try {
      const p = normalizePayload(await api.post<UpdatesPayload>('/api/updates/check'))
      setData(p)
      toast.push(p.check.error ? 'The check finished with an error' : p.check.available ? `Version ${p.check.latest} is available` : 'Opskest is up to date', p.check.error ? 'warn' : 'ok')
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) toast.push(`Checked a moment ago, try again in ${plural(e.retryAfter ?? 10, 'second', 'seconds')}`, 'neutral')
      else if (e instanceof ApiError && e.status === 409) { toast.push(e.message || 'Update checks are not available right now', 'warn'); void peek() }
      else toast.push(errorText(e, 'Could not check for updates'), 'danger')
    } finally {
      setChecking(false)
    }
  }

  async function startOperation(kind: 'install' | 'rollback') {
    if (!data) return
    try {
      // The accepted payload already carries the new phase, so a stale "failed" cannot end the wait early.
      const accepted = await api.post<UpdatesPayload>(`/api/updates/${kind}`)
      if (accepted && typeof accepted.current === 'string') setData(normalizePayload(accepted))
    } catch (e) {
      toast.push(errorText(e, kind === 'install' ? 'Could not start the update' : 'Could not start the rollback'), 'danger')
      setConfirm(null)
      return
    }
    setConfirm(null)
    startWait(kind, data.current)
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    const fe: FieldErrors = {}
    const url = feedUrl.trim()
    if (enabled && mode !== 'off' && !url) fe.feedUrl = 'Enter the address of the release manifest.'
    else if (url && !validFeedUrl(url)) fe.feedUrl = 'Enter a full web address: https, or http only for localhost.'
    if (savedKey && keyMode === 'replace' && keyValue.trim() === '') fe.publicKey = 'Paste the new key, or cancel Replace.'
    setErrors(fe)
    if (Object.keys(fe).length > 0) return

    const body: UpdateSettingsIn = {
      enabled, feedUrl: url, mode, autoHour,
      publicKey: keyMode === 'clear' ? '' : keyMode === 'replace' && keyValue.trim() !== '' ? keyValue.trim() : null,
    }
    setSaving(true)
    try {
      const p = normalizePayload(await api.put<UpdatesPayload>('/api/updates/settings', body))
      setData(p); syncForm(p.settings)
      toast.push('Update settings saved', 'ok')
    } catch (err) {
      if (err instanceof ApiError && err.fields && Object.keys(err.fields).length > 0) {
        setErrors(err.fields)
        toast.push(err.message || 'Check the highlighted fields', 'danger')
      } else {
        toast.push(errorText(err, 'Could not save the update settings'), 'danger')
      }
    } finally {
      setSaving(false)
    }
  }

  if (error) return <SettingsSection><ErrorRetry message={`Could not load update information. ${error}`} onRetry={load} /></SettingsSection>
  if (!data) return <SettingsLoading label="Loading update information" sections={2} rows={3} />

  const { check, settings, state } = data
  const now = Date.now()
  const dev = isDevVersion(data.current)
  const busyOp = wait != null || isActivePhase(state.phase)
  const docker = data.installMethod === 'docker'
  const canShowInstall = check.available && !!check.latest && !dev
  const blocked = canShowInstall && check.minVersionBlocked
  const showDocker = canShowInstall && (docker || (!data.canInstall && !!data.dockerHint))
  const canInstallHere = canShowInstall && !showDocker && !blocked && data.canInstall
  const placed = new Set(['enabled', 'feedUrl', 'mode', 'autoHour', 'publicKey'])
  const unplaced = Object.entries(errors).filter(([k]) => !placed.has(k))
  const tz = serverTimeZone()
  const notes = check.latest ? parseNotes(check.notes) : []
  const prev = vTag(data.previousVersion)
  const exitMode = data.restartMode === 'exit'

  // ── progress ──
  const inFlight = wait != null || outcome != null || isActivePhase(state.phase)
  const showProgress = inFlight || state.phase === 'failed'
  const livePhase = unreachable ? 'restarting' : wait && state.phase === 'idle' ? 'starting' : state.phase
  const steps = wait?.kind === 'rollback' ? INSTALL_STEPS.filter(s => s.phase !== 'downloading') : INSTALL_STEPS

  // ── status ──
  const checksOn = settings.enabled && settings.mode !== 'off' && !!settings.feedUrl
  const recentFailure = data.lastInstallFailedAt != null && now - data.lastInstallFailedAt < DAY_MS && settings.mode === 'auto'
  const showCheckError = !!check.error && !(check.available && check.latest)
  const nextAt = checksOn ? data.nextCheckAt : null

  const statusBadge = dev ? <Badge dot>Updates disabled</Badge>
    : showCheckError ? <Badge tone="danger" dot>Check failed</Badge>
    : canShowInstall ? <Badge tone="accent" dot>Version {check.latest} available</Badge>
    : !check.checkedAt ? <Badge dot>Not checked yet</Badge>
    : <Badge tone="ok" dot>Up to date</Badge>
  const rollbackLabel = prev ? `Roll back to ${prev}` : 'Roll back'

  // One muted sentence: when it last looked and when it looks next.
  const sentence = (
    <>
      {check.checkedAt ? <>Last checked <span title={dateServer(check.checkedAt)}>{ago(check.checkedAt)}</span>.</> : 'No check has run yet.'}
      {' '}
      {!checksOn ? 'Automatic checks are off.' : nextAt == null ? 'The next automatic check is scheduled soon.' : nextAt <= now ? 'The next automatic check is due now.' : <>Next automatic check <span title={dateServer(nextAt)}>in {duration(nextAt - now)}</span>.</>}
    </>
  )

  const meta = [
    check.publishedAt ? `Published ${dateServer(check.publishedAt)}` : '',
    check.asset ? `${formatBytes(check.asset.size)} (${check.asset.os}/${check.asset.arch})` : '',
    check.signatureVerified ? 'signature verified' : settings.publicKeySet ? 'signature not verified' : 'not verified: no release key',
  ].filter(Boolean).join(' · ')

  const keyLine = settings.publicKeySet
    ? <>Release key: {settings.publicKeySource === 'build' ? 'built into this binary' : 'entered in settings'}{settings.publicKeyFingerprint && <> (fingerprint <span className="font-mono">{settings.publicKeyFingerprint}</span>)</>}.{settings.publicKeySource === 'build' && ' A key pasted here takes precedence.'}</>
    : <>Release key: none yet. Without one, updates are announced but cannot be installed.</>
  const dockerCommands = (data.dockerHint || 'docker compose pull && docker compose up -d').split('&&').map(c => c.trim()).filter(Boolean)
  const settingsSummary = dirty ? 'Unsaved changes' : settings.enabled ? `${MODE_TEXT[settings.mode]}${feedHost(settings.feedUrl) ? ` · ${feedHost(settings.feedUrl)}` : ''}` : 'Checks are off'

  return (
    <>
      <SettingsSection id="updates" className="scroll-mt-24" title="Updates" description={sentence}
        actions={<>
          {statusBadge}
          {data.canRollback && (
            <Button size="sm" variant="secondary" disabled={busyOp} onClick={() => setConfirm('rollback')}>{rollbackLabel}</Button>
          )}
          <Button variant={canInstallHere ? 'secondary' : 'primary'} busy={checking} disabled={dev || busyOp || !settings.enabled || !settings.feedUrl}
            title={dev ? 'Update checks are disabled in a development build' : busyOp ? 'An update is running' : !settings.enabled ? 'Turn on update checks first' : !settings.feedUrl ? 'Set the update source first' : undefined}
            icon={checking ? undefined : <Icon.Refresh className="w-3.5 h-3.5" />} onClick={checkNow}>
            Check now
          </Button>
          {canInstallHere && <Button variant="primary" disabled={busyOp} onClick={() => setConfirm('install')}>Install update</Button>}
        </>}>
        {!showProgress && (
          <>
            {dev && (
              <Alert tone="accent">
                <b>Development build.</b> This copy of Opskest was built without a release version number, so it cannot compare itself with releases and updates are disabled.
              </Alert>
            )}

            {showCheckError && (
              <Alert tone="danger">
                <p>The last update check failed.</p>
                <p className="mt-1 font-mono text-[12px] [overflow-wrap:anywhere]">{check.error}</p>
              </Alert>
            )}

            {blocked && (
              <Alert tone="warn">
                <b>This release needs at least {vTag(check.minVersion)} first.</b>{' '}
                Update to {vTag(check.minVersion)} or a later release, then install {vTag(check.latest)}.
              </Alert>
            )}

            {canShowInstall && !showDocker && !blocked && !data.canInstall && (
              <Alert tone="warn">
                <b>This installation cannot update itself.</b>{' '}
                {data.cannotInstallReason || 'Opskest does not have permission to replace its own program file.'}{' '}
                Update it manually, or fix that and check again.
              </Alert>
            )}

            {showDocker && (
              <div className="space-y-3 rounded-[14px] border border-default bg-surface-2/40 px-4 py-4">
                <h4 className="text-sm font-semibold text-body">Update with docker compose pull</h4>
                <p className="text-[13px] text-body-2">Opskest runs in a container here, so it cannot replace itself. Run these in the folder that holds your <span className="font-mono text-[12px]">docker-compose.yml</span>, one after the other.</p>
                <div className="space-y-2">
                  {dockerCommands.map((c, i) => <CopyField key={i} label={dockerCommands.length > 1 ? `Command ${i + 1}` : 'Update command'} text={c} />)}
                </div>
                <SoftNote>Your data and backups live on the host, so they are kept. Take a backup first (Settings, Backups) if you want a restore point. To roll back, set the previous image tag in docker-compose.yml and run <span className="font-mono">docker compose up -d</span> again; restore the backup if the older version refuses the database.</SoftNote>
              </div>
            )}

            {canShowInstall && <WhatsNew meta={meta} blocks={notes} />}

            {check.error && check.available && check.latest && (
              <SoftNote>The last check could not reach the update source: <span className="font-mono">{check.error}</span>. The update above was found earlier.</SoftNote>
            )}

            <Collapsible title="Update settings" summary={settingsSummary} open={settingsOpen ?? false} onOpenChange={setSettingsOpen}>
              <form onSubmit={save} noValidate aria-label="Update settings">
                <FieldGrid>
                  <div className="flex min-w-0 flex-col gap-5">
                    <Switch label="Check for updates" checked={enabled} onChange={e => setEnabled(e.target.checked)}
                      hint="Looks for a newer release once a day." />

                    <Field label="Update source" error={errors.feedUrl}
                      hint="The address of the release manifest.json. https, or http only for localhost. Opskest only reads it; it never sends your data there.">
                      {id => (
                        <Input id={id} type="url" inputMode="url" autoComplete="off" spellCheck={false} autoCapitalize="off"
                          className="font-mono" disabled={!enabled}
                          placeholder="https://github.com/opskest/opskest/releases/latest/download/manifest.json"
                          value={feedUrl} aria-invalid={!!errors.feedUrl || undefined} onChange={e => setFeedUrl(e.target.value)} />
                      )}
                    </Field>
                  </div>

                  <div className="flex min-w-0 flex-col gap-5">
                    <Field label="When a new version is found" error={errors.mode}
                      hint={mode === 'auto' ? undefined : 'What Opskest does after a check finds a newer release.'}>
                      {id => (
                        <Select id={id} value={mode} disabled={!enabled} aria-invalid={!!errors.mode || undefined} onChange={e => setMode(e.target.value as UpdateMode)}>
                          <option value="off">Off: never check</option>
                          <option value="notify">Notify me: check daily and show a banner</option>
                          <option value="auto">Install automatically: install new versions at {hh(autoHour)} ({tz})</option>
                        </Select>
                      )}
                    </Field>
                    {mode === 'auto' && (
                      <Field label={`Install at (${tz})`} error={errors.autoHour} hint="Opskest restarts for about a minute at this hour, in the server time zone.">
                        {id => (
                          <Select id={id} value={autoHour} disabled={!enabled} aria-invalid={!!errors.autoHour || undefined} onChange={e => setAutoHour(Number(e.target.value))}>
                            {HOURS.map(h => <option key={h} value={h}>{hh(h)}</option>)}
                          </Select>
                        )}
                      </Field>
                    )}

                    <Field label="Release public key" error={errors.publicKey} optional
                      hint="Confirms a download was signed by the project before it is installed. A key saved here is never shown again.">
                      {id => {
                        let control: ReactNode
                        if (savedKey && keyMode === 'keep') {
                          control = (
                            <div id={id} role="group" aria-label="Release public key: a key is saved" className="flex min-h-10 flex-wrap items-center gap-2 rounded-[10px] border border-dashed border-default bg-surface-2/50 px-3 py-1.5 text-[13px] text-muted">
                              <Icon.Lock className="w-3.5 h-3.5 text-faint" />
                              <span>A key is saved</span>
                              <span className="flex-1" />
                              <button type="button" className="font-medium text-accent hover:underline underline-offset-2" onClick={() => { setKeyMode('replace'); setKeyValue('') }}>Replace</button>
                              <span aria-hidden="true" className="text-faint">·</span>
                              <button type="button" className="font-medium text-muted hover:text-danger" onClick={() => { setKeyMode('clear'); setKeyValue('') }}>Remove</button>
                            </div>
                          )
                        } else if (savedKey && keyMode === 'clear') {
                          control = (
                            <div id={id} role="group" aria-label="Release public key: will be removed" className="flex min-h-10 items-center gap-2 rounded-[10px] border border-dashed border-danger px-3 py-1.5 text-[13px] text-danger">
                              The key will be removed when you save
                              <span className="flex-1" />
                              <button type="button" className="font-medium underline underline-offset-2" onClick={() => setKeyMode('keep')}>Undo</button>
                            </div>
                          )
                        } else {
                          control = (
                            <div className="flex flex-col gap-2">
                              <Textarea id={id} rows={3} className="font-mono text-[12px] break-all" autoComplete="off" spellCheck={false} autoCapitalize="off"
                                placeholder={savedKey ? 'Paste the new key' : 'Paste the release public key'}
                                value={keyValue} aria-invalid={!!errors.publicKey || undefined} onChange={e => setKeyValue(e.target.value)} />
                              {savedKey && <div><Button size="sm" variant="ghost" onClick={() => { setKeyMode('keep'); setKeyValue('') }}>Cancel replace</Button></div>}
                            </div>
                          )
                        }
                        return (
                          <div className="flex flex-col gap-2">
                            {control}
                            <SoftNote>{keyLine}</SoftNote>
                          </div>
                        )
                      }}
                    </Field>
                  </div>
                </FieldGrid>

                {(data.autoSkipVersion || recentFailure) && (
                  <div className="mt-4 space-y-1">
                    {data.autoSkipVersion && <SoftNote>Automatic installs will skip {vTag(data.autoSkipVersion)} after the rollback. Install it yourself from this page if you want it.</SoftNote>}
                    {recentFailure && <SoftNote>The last install failed {ago(data.lastInstallFailedAt!)}. Automatic installs wait 24 hours after a failure.</SoftNote>}
                  </div>
                )}

                {unplaced.length > 0 && (
                  <div className="mt-4">
                    <Alert tone="danger">
                      {unplaced.map(([k, val]) => <div key={k}>{k}: {val}</div>)}
                    </Alert>
                  </div>
                )}
                {errors.enabled && <div className="mt-4"><FieldMessage error={errors.enabled} /></div>}

                <SettingsFooter status={dirty ? <span className="text-warn">Unsaved changes</span> : undefined}>
                  {dirty && <Button variant="ghost" onClick={() => syncForm(settings)}>Discard</Button>}
                  <Button type="submit" variant="primary" busy={saving} disabled={!dirty}>Save settings</Button>
                </SettingsFooter>
              </form>
            </Collapsible>
          </>
        )}
      </SettingsSection>

      {/* Progress */}
      {showProgress && (
        <SettingsSection title={state.phase === 'failed' && !inFlight ? 'Update failed' : wait?.kind === 'rollback' ? 'Rolling back' : 'Update in progress'}>
          {outcome === 'reloading' ? (
            <p role="status" className="text-[13px] text-body-2">Opskest is back. Reloading this page…</p>
          ) : outcome === 'timeout' ? (
            <>
              <Alert tone="warn">It is taking longer than expected. Opskest has not answered for 90 seconds. It may still be starting; reload to check, and look at the server logs if it does not come back.{exitMode && ' Your service manager has to start the new version, so check that it is running.'}</Alert>
              <SettingsFooter><Button variant="primary" onClick={() => window.location.reload()}>Reload</Button></SettingsFooter>
            </>
          ) : outcome === 'stalled' ? (
            <>
              <Alert tone="warn">The update finished, but Opskest is still running version {data.current}. Check the server logs for the reason.</Alert>
              <SettingsFooter><Button variant="secondary" onClick={() => { setOutcome(null); load() }}>Dismiss</Button></SettingsFooter>
            </>
          ) : state.phase === 'failed' && !inFlight ? (
            <>
              <Alert tone="danger">
                <p>The update did not complete. Opskest is still running version {data.current}.</p>
                {state.error && <p className="mt-1 font-mono text-[12px]">{state.error}</p>}
              </Alert>
              {state.backupName && (
                <div className="mt-3"><SoftNote>A database backup was taken first: <span className="font-mono">{state.backupName}</span>. It is listed under Settings, Backups.</SoftNote></div>
              )}
              {canInstallHere && (
                <SettingsFooter status="The error stays here until you try again.">
                  <Button variant="primary" disabled={busyOp} onClick={() => setConfirm('install')}>Try again</Button>
                </SettingsFooter>
              )}
            </>
          ) : (
            <div className="space-y-4">
              <div role="status" aria-live="polite" className="sr-only">{livePhase === 'starting' ? 'Starting' : phaseLabel(livePhase)}</div>
              <StepList phase={livePhase} percent={state.percent} steps={steps} />
              {state.backupName && <SoftNote>Database backup: <span className="font-mono">{state.backupName}</span></SoftNote>}
              {livePhase === 'restarting' ? (
                <div className="space-y-2">
                  <Alert tone={exitMode ? 'warn' : 'accent'}>
                    {exitMode && <p>This page stays unavailable until your service manager starts the new version.</p>}
                    <p className={exitMode ? 'mt-1' : undefined}>{data.restartHint || 'Opskest is restarting itself; the page reconnects in a few seconds.'}</p>
                  </Alert>
                  <SoftNote>
                    {unreachable
                      ? 'Waiting for Opskest to answer. Trying again every 1.5 seconds for up to 90 seconds. Monitoring is interrupted until then.'
                      : 'Monitoring is interrupted until Opskest answers again. This page reloads by itself.'}
                  </SoftNote>
                </div>
              ) : (
                <SoftNote>Keep this page open. Monitoring is briefly interrupted when Opskest restarts.</SoftNote>
              )}
            </div>
          )}
        </SettingsSection>
      )}

      <ConfirmModal
        open={confirm === 'install'}
        title={`Install version ${check.latest}?`}
        description={(
          <div className="space-y-2">
            <p>Opskest will update from <span className="font-mono">{data.current}</span> to <span className="font-mono">{check.latest}</span>. In order:</p>
            <ol className="list-decimal pl-5 space-y-1">
              <li>The database is backed up first.</li>
              <li>The new version is downloaded and its checksum {check.signatureVerified ? 'and signature are' : 'is'} verified.</li>
              <li>{exitMode ? 'Opskest shuts down and your service manager starts the new version.' : 'Opskest restarts.'} Monitoring stops for about a minute.</li>
              <li>The previous version is kept so you can roll back.</li>
            </ol>
          </div>
        )}
        confirmLabel={`Install ${check.latest}`}
        tone="primary"
        onConfirm={() => startOperation('install')}
        onClose={() => setConfirm(null)}
      />
      <ConfirmModal
        open={confirm === 'rollback'}
        title={prev ? `Roll back to ${prev}?` : 'Roll back to the previous version?'}
        description={(
          <div className="space-y-2">
            <p>Opskest will swap its program files with the previous version{prev ? ` (${prev})` : ''} and restart. The version running now is kept, so you can switch back again.</p>
            <p>Monitoring stops for about a minute. {exitMode ? 'Your service manager has to start the older version.' : 'This page reloads when the older version answers.'}</p>
            <p>Opskest tries to back up the database first. If the older version refuses to open it, restore that backup under Settings, Backups.</p>
          </div>
        )}
        confirmLabel="Roll back"
        tone="primary"
        onConfirm={() => startOperation('rollback')}
        onClose={() => setConfirm(null)}
      />
    </>
  )
}
