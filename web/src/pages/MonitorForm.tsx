import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { GeneralSection, TypeIcon, TypePicker } from '../components/form/BasicsSection'
import { Section } from '../components/form/primitives'
import { AlertingSection, TimingSection } from '../components/form/TimingAlerting'
import { TypeFields } from '../components/form/TypeFields'
import { PageTitle } from '../components/PageShell'
import { TestResult } from '../components/TestResult'
import { Alert, Button, EmptyState, ErrorRetry, Icon, SkeletonBlock, useToast } from '../components/ui'
import { applyTemplate, type MonitorTemplate } from '../lib/templates'
import { TemplatePicker } from '../components/form/TemplatePicker'
import { RegionField } from '../components/form/RegionField'
import { api, ApiError, errorText, unwrapList } from '../lib/api'
import {
  detectVendorProvider, draftFromOut, MONITOR_TYPES, newDraft, toMonitorIn, unplacedErrors, validateDraft,
  type Draft, type HeaderRow, type SecretDraft, type SecretKey, type SettingsDraft,
} from '../lib/monitorForm'
import { normGroup, normResult, normTag } from '../lib/normalize'
import type { Channel, FieldErrors, Group, MonitorOut, Result, Tag } from '../lib/types'

type Load = 'loading' | 'ready' | 'notfound' | 'error'

export default function MonitorForm({ id: idParam }: { id?: string }) {
  const search = useSearch()
  const [, navigate] = useLocation()
  const { push } = useToast()
  const [tags, setTags] = useState<Tag[]>([])
  useEffect(() => {
    api.get<unknown>('/api/tags')
      .then(d => setTags(unwrapList(d, 'tags').map(normTag)))
      .catch(() => {})
  }, [])

  const isEdit = idParam != null
  const id = Number(idParam)
  const prefillGroup = useMemo(() => {
    const g = Number(new URLSearchParams(search).get('groupId'))
    return Number.isFinite(g) && g > 0 ? g : null
  }, [search])

  const [draft, setDraft] = useState<Draft>(() => newDraft(prefillGroup))
  const [section, setSection] = useState('endpoint')
  const [templates, setTemplates] = useState<MonitorTemplate[] | null>(null)
  const [templatesFailed, setTemplatesFailed] = useState(false)
  const [templateId, setTemplateId] = useState<number | null>(null)
  const [original, setOriginal] = useState<MonitorOut | null>(null)
  const [load, setLoad] = useState<Load>(isEdit ? 'loading' : 'ready')
  const [loadError, setLoadError] = useState('')
  const [reload, setReload] = useState(0)

  const [groups, setGroups] = useState<Group[]>([])
  const [channels, setChannels] = useState<Channel[] | null>(null)
  const [listsError, setListsError] = useState<string | null>(null)
  const [listsReload, setListsReload] = useState(0)
  const channelsTouched = useRef(false)
  const providerPicked = useRef(false)

  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState<string | null>(null)
  const [focusNonce, setFocusNonce] = useState(0)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<{ result: Result | null; error: string | null } | null>(null)
  const [targetTouched, setTargetTouched] = useState(false)

  const rootRef = useRef<HTMLDivElement>(null)
  const resultRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!isEdit) return
    if (!Number.isFinite(id)) { setLoad('notfound'); return }
    let alive = true
    setLoad('loading')
    api.get<MonitorOut>(`/api/monitors/${id}`)
      .then(m => {
        if (!alive) return
        setOriginal(m)
        setDraft(draftFromOut(m))
        providerPicked.current = true
        setLoad('ready')
      })
      .catch(err => {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) setLoad('notfound')
        else { setLoadError(errorText(err, 'Could not load this monitor.')); setLoad('error') }
      })
    return () => { alive = false }
  }, [isEdit, id, reload])

  // Groups and channels for the pickers; a failure leaves them empty with a retry.
  useEffect(() => {
    let alive = true
    const failed: string[] = []
    Promise.all([
      api.get<unknown>('/api/groups').then(d => unwrapList(d, 'groups').map(normGroup)).catch(() => { failed.push('groups'); return [] as Group[] }),
      api.get<unknown>('/api/channels').then(d => unwrapList<Channel>(d, 'channels')).catch(() => { failed.push('channels'); return [] as Channel[] }),
      api.get<unknown>('/api/templates').then(d => unwrapList<MonitorTemplate>(d, 'templates')).catch(() => null),
    ]).then(([g, c, tpls]) => {
      if (!alive) return
      setTemplates(tpls ?? [])
      setTemplatesFailed(tpls === null)
      setGroups(g.sort((a, b) => (a.sort - b.sort) || a.name.localeCompare(b.name)))
      setChannels(c)
      setListsError(failed.length ? `Couldn't load ${failed.join(' and ')} — those pickers are empty.` : null)
    })
    return () => { alive = false }
  }, [listsReload])

  // New monitors start with the default channels ticked.
  useEffect(() => {
    if (isEdit || !channels || channelsTouched.current) return
    setDraft(d => ({ ...d, channelIds: channels.filter(c => c.isDefault).map(c => c.id) }))
  }, [isEdit, channels])

  useEffect(() => {
    if (focusNonce === 0) return
    const el = rootRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]')
    if (el) {
      el.focus()
      el.scrollIntoView({ block: 'center', behavior: 'smooth' })
    } else {
      rootRef.current?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    }
  }, [focusNonce])

  useEffect(() => {
    if (test) resultRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [test])

  function clearErrors(keys: string[], prefix?: RegExp) {
    setErrors(e => {
      let changed = false
      const next = { ...e }
      for (const k of Object.keys(next)) {
        if (keys.includes(k) || (prefix && prefix.test(k))) { delete next[k]; changed = true }
      }
      return changed ? next : e
    })
  }

  function patch(p: Partial<Draft>) {
    setDraft(d => {
      const next = { ...d, ...p }
      // A vendor feed's provider follows its URL until picked by hand.
      if (p.target !== undefined && next.type === 'vendor' && !providerPicked.current) {
        next.settings = { ...next.settings, vendorProvider: detectVendorProvider(p.target) }
      }
      return next
    })
    clearErrors(Object.keys(p))
  }

  function patchSettings(p: Partial<SettingsDraft>) {
    setDraft(d => ({ ...d, settings: { ...d.settings, ...p } }))
    clearErrors(Object.keys(p).map(k => `settings.${k}`))
  }

  function check(): boolean {
    const fe = validateDraft(draft)
    setErrors(fe)
    if (Object.keys(fe).length > 0) {
      const first = Object.keys(fe)[0]
      setSection(first.startsWith('settings.region') ? 'regions' : ['intervalS','timeoutMs','retries','retryIntervalS','degradedMs','sslWarnDays','settings.certThresholds'].includes(first) ? 'schedule' : ['channelIds','reminderMin','runbook'].includes(first) || first.startsWith('settings.dependsOn') || first.startsWith('settings.latencyAlert') ? 'alerts' : first.startsWith('settings.') || first.startsWith('headers') || first.startsWith('secrets.') ? 'request' : 'endpoint')
      setFormError('Fix the highlighted fields first.')
      setFocusNonce(n => n + 1)
      return false
    }
    setFormError(null)
    return true
  }

  function applyServerError(err: unknown, fallback: string): string {
    if (err instanceof ApiError && err.fields && Object.keys(err.fields).length > 0) {
      setErrors(err.fields)
      setSection('all')
      setFocusNonce(n => n + 1)
      return 'The server rejected some fields — see the highlighted ones.'
    }
    return errorText(err, fallback)
  }

  async function testNow() {
    setTest(null)
    if (!check()) return
    setTesting(true)
    try {
      const body = isEdit ? { ...toMonitorIn(draft), id } : toMonitorIn(draft)
      setTest({ result: normResult(await api.post<unknown>('/api/monitors/test', body)), error: null })
    } catch (err) {
      setTest({ result: null, error: applyServerError(err, 'The test could not run.') })
    } finally {
      setTesting(false)
    }
  }

  async function save() {
    if (!check()) return
    setSaving(true)
    try {
      const body = toMonitorIn(draft)
      const res = isEdit
        ? await api.put<MonitorOut>(`/api/monitors/${id}`, body)
        : await api.post<MonitorOut>('/api/monitors', body)
      const savedId = res && typeof res.id === 'number' ? res.id : id
      push(isEdit ? `Saved ${body.name}.` : `Created ${body.name}.`, 'ok')
      navigate(Number.isFinite(savedId) ? `/monitors/${savedId}` : '/')
    } catch (err) {
      setFormError(applyServerError(err, 'Could not save this monitor.'))
      setSaving(false)
    }
  }

  if (load === 'loading') return <SkeletonBlock lines={6} label="Loading monitor" />
  if (load === 'notfound') {
    return <EmptyState heading title="Monitor not found" action={<Button onClick={() => navigate('/monitors')}>Back to monitors</Button>}>It may have been deleted.</EmptyState>
  }
  if (load === 'error') return <ErrorRetry message={loadError} onRetry={() => setReload(n => n + 1)} />

  const d = draft
  const other = unplacedErrors(errors, d.type)
  const typeMeta = MONITOR_TYPES.find(t => t.type === d.type)
  const liveTargetError = targetTouched && d.target.trim() !== '' ? validateDraft(d).target : undefined
  const cancelHref = isEdit ? `/monitors/${id}` : '/monitors'
  const title = isEdit ? `Edit ${original?.name ?? 'monitor'}` : 'Add monitor'

  return (
    <div ref={rootRef} className="w-full flex flex-col gap-6 scroll-mt-20">
      <PageTitle
        back={isEdit ? { href: `/monitors/${id}`, label: original?.name ?? `Monitor ${id}` } : { href: '/monitors', label: 'All monitors' }}
        title={title}
        actions={<span className="hidden sm:flex items-center gap-2">
          <Button variant="ghost" onClick={() => navigate(cancelHref)}>Cancel</Button>
          <Button variant="primary" onClick={save} busy={saving} disabled={testing}>{isEdit ? 'Save changes' : 'Create monitor'}</Button>
        </span>}
      >
        {isEdit ? `${typeMeta?.label ?? d.type} monitor · changes apply on the next check.` : 'Pick a type, fill in the target, and test it before you save.'}
      </PageTitle>

      {(formError || other.length > 0) && (
        <Alert tone="danger" onDismiss={() => setFormError(null)}>
          {formError && <span className="block font-medium">{formError}</span>}
          {other.map(([k, v]) => <span key={k} className="block"><span className="font-mono">{k}</span>: {v}</span>)}
        </Alert>
      )}
      {listsError && <ErrorRetry message={listsError} onRetry={() => setListsReload(n => n + 1)} />}

      <div className="grid lg:grid-cols-[200px_minmax(0,1fr)] 2xl:grid-cols-[220px_minmax(0,1fr)_240px] gap-7 items-start">
        <aside className="min-w-0 lg:sticky lg:top-24"><nav aria-label="Monitor configuration" className="flex flex-wrap lg:flex-col lg:flex-nowrap gap-1">{[
          ['endpoint','Service & endpoint'],['request','Check configuration'],...((d.type === 'http' || d.type === 'ping' || d.type === 'dns') && !d.settings.vendor ? [['regions','Check locations']] : []),['schedule','Check schedule'],['alerts','Alerts & dependencies'],['all','All settings']
        ].map(([key,label]) => <button key={key} type="button" aria-current={section === key ? 'page' : undefined} onClick={() => setSection(key)} className={`shrink-0 h-10 text-left rounded-[10px] px-3 text-sm transition-colors ${section === key ? 'bg-surface-2 text-body font-semibold' : 'text-muted hover:text-body hover:bg-surface'}`}>{label}</button>)}</nav></aside>
        <div className="min-w-0 space-y-6">
      {(section === 'endpoint' || section === 'all') && <>
      <TemplatePicker
        draft={d}
        templates={templates}
        loadError={templatesFailed}
        selectedId={templateId}
        onChange={setTemplates}
        onApply={isEdit ? undefined : t => {
          setTemplateId(t?.id ?? null)
          if (t) {
            channelsTouched.current = true
            setDraft(x => applyTemplate(x, t))
            setTest(null)
            setTargetTouched(false)
            clearErrors(['type', 'target'], /^settings\./)
          }
        }}
      />

      <Section
        title="Monitor type"
        hint={isEdit ? "The type can't change after a monitor is created — duplicate it instead." : 'Choose the check protocol. A vendor status-page URL on HTTP is detected automatically.'}
        icon={<Icon.Grid className="w-3.5 h-3.5" />}
      >
        <TypePicker
          value={d.type}
          locked={isEdit}
          onChange={t => {
            setDraft(x => ({
              ...x,
              type: t,
              settings: t === 'vendor' && !providerPicked.current && x.target ? { ...x.settings, vendorProvider: detectVendorProvider(x.target) } : x.settings,
            }))
            setTest(null)
            setTargetTouched(false)
            clearErrors(['target', 'type'], /^settings\./)
          }}
        />
      </Section>

      <GeneralSection
        draft={d}
        errors={errors}
        groups={groups}
        tags={tags}
        onTagsChange={setTags}
        onPatch={patch}
        onGroupCreated={g => setGroups(prev => [...prev.filter(x => x.id !== g.id), g])}
        targetError={liveTargetError}
        onTargetBlur={() => setTargetTouched(true)}
      />

      </>}
      {(section === 'request' || section === 'all') && <>
      <Section
        title={`${typeMeta?.label ?? 'Type'} settings`}
        hint={TYPE_HINT[d.type]}
        icon={<TypeIcon type={d.type} className="w-3.5 h-3.5" />}
      >
        <TypeFields
          type={d.type}
          settings={d.settings}
          onSettings={patchSettings}
          errors={errors}
          headers={d.headers}
          onHeaders={(rows: HeaderRow[]) => { setDraft(x => ({ ...x, headers: rows })); clearErrors([], /^(settings\.)?headers(\.|$)/) }}
          basicPass={d.basicPass}
          bearerToken={d.bearerToken}
          proxyPass={d.proxyPass}
          clientCert={d.clientCert}
          clientKey={d.clientKey}
          oauthSecret={d.oauthSecret}
          selfId={Number.isFinite(id) ? id : undefined}
          onSecret={(which: SecretKey, s: SecretDraft) => {
            setDraft(x => ({ ...x, [which]: s }))
            clearErrors([`secrets.${which}`, ...(which === 'clientKey' ? ['secrets.clientCert'] : [])])
          }}
          pushUrl={original?.pushUrl || undefined}
          target={d.target}
          providerAuto={!providerPicked.current && d.target.trim() !== ''}
          onProviderPicked={() => { providerPicked.current = true }}
        />
      </Section>

      </>}
      {(section === 'regions' || section === 'all') && <>
      {(d.type === 'http' || d.type === 'ping' || d.type === 'dns') && !d.settings.vendor && (
        <Section title="Monitoring region" hint="Where this monitor is checked from. Worldwide checks run on the free Globalping network." icon={<Icon.Globe className="w-3.5 h-3.5" />}>
          <RegionField mode={d.settings.regionMode} countries={d.settings.regionCountries}
            error={errors['settings.regionCountries'] || errors['settings.regionMode']}
            onChange={(regionMode, regionCountries) => patchSettings({ regionMode, regionCountries })} />
        </Section>
      )}

      </>}
      {(section === 'schedule' || section === 'all') && 
      <TimingSection draft={d} errors={errors} onPatch={patch} />}
      {(section === 'alerts' || section === 'all') && <AlertingSection draft={d} errors={errors} channels={channels} onPatch={patch} onChannelsTouched={() => { channelsTouched.current = true }} selfId={Number.isFinite(id) ? id : undefined} />}

        </div>
        <aside className="lg:col-start-2 2xl:col-start-auto 2xl:sticky 2xl:top-24 rounded-[14px] bg-surface border border-default p-5 space-y-5"><div><h2 className="text-base font-semibold">Monitor summary</h2><p className="text-sm text-muted mt-3 break-words">{d.name || 'Untitled monitor'}</p><p className="font-mono text-xs text-muted mt-2 break-all">{d.target || 'No endpoint configured'}</p></div><dl className="text-sm divide-y divide-default"><div className="flex justify-between py-3"><dt className="text-muted">Type</dt><dd>{typeMeta?.label ?? d.type}</dd></div><div className="flex justify-between py-3"><dt className="text-muted">Interval</dt><dd>{Number.isFinite(d.intervalS) ? `${d.intervalS}s` : '—'}</dd></div><div className="flex justify-between py-3"><dt className="text-muted">Alerts</dt><dd>{d.channelIds.length} channels</dd></div></dl><p className="text-xs text-muted leading-6">{isEdit ? 'Saved changes take effect on the next scheduled check.' : 'Run a check before creating the monitor to verify its configuration.'}</p></aside>
      </div>
      <div ref={resultRef} className="scroll-mb-28 empty:hidden">
        {test && <TestResult result={test.result} error={test.error} onDismiss={() => setTest(null)} />}
      </div>

      <div className="sticky bottom-[calc(76px+env(safe-area-inset-bottom))] lg:bottom-3 z-10 mt-1 rounded-[14px] border border-default bg-surface p-2.5 sm:p-3 sm:px-4 grid grid-cols-[auto_auto_minmax(0,1fr)] sm:flex items-center gap-2">
        {d.type !== 'push' && d.type !== 'manual'
          ? (
            <>
              <Button className="min-h-11 sm:min-h-10" onClick={testNow} busy={testing} disabled={saving} icon={<Icon.Bolt className="w-3.5 h-3.5" />}>Test now</Button>
              <span className="text-[12px] text-muted hidden md:inline">Runs the check once — nothing is saved.</span>
            </>
          )
          : <span className="text-[12px] text-muted hidden md:inline">{d.type === 'push' ? 'Push monitors wait for your job — there is nothing to test yet.' : 'Manual monitors run no check — set the status from the monitor page.'}</span>}
        <span className="hidden sm:block flex-1" />
        <Button className="min-h-11 sm:min-h-10" variant="ghost" onClick={() => navigate(cancelHref)}>Cancel</Button>
        <Button className="w-full sm:w-auto min-h-11 sm:min-h-10" variant="primary" onClick={save} busy={saving} disabled={testing}>{isEdit ? 'Save changes' : 'Create monitor'}</Button>
      </div>
    </div>
  )
}

const TYPE_HINT: Record<string, string> = {
  http: 'What makes a response healthy, plus the request details.',
  tcp: 'Opens a connection to the port; optionally completes a TLS handshake.',
  ping: 'Sends ICMP echo requests and measures loss and round trip.',
  dns: 'Resolves the name and optionally checks the answer.',
  push: 'Your job calls a unique URL; the monitor fails when it stops.',
  websocket: 'Opens the socket, optionally sends a message and checks the reply.',
  vendor: "Reads the vendor's own incident feed.",
  group: 'Follows other monitors and shows as one status.',
  manual: 'Nothing is checked; you set the status.',
}
