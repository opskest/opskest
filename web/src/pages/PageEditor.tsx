import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useLocation, useSearch } from 'wouter'
import { PageTitle, Crumbs } from '../components/PageShell'
import { Button, buttonClass, ConfirmModal, EmptyState, ErrorRetry, Icon, useToast } from '../components/ui'
import { RailNav, SideNavLayout, type RailGroup } from '../components/RailNav'
import { SettingsLoading, SettingsPanel } from '../components/settings/SettingsPanel'
import { Skeleton } from '../components/skeleton'
import { AccessSection, AppearanceSection, BasicsSection, ContentSection, ServicesSection, UpdatesSection, type SectionProps } from '../components/pages/EditorSections'
import { CreateFlow } from '../components/pages/CreateFlow'
import { useLeaveGuard } from '../components/pages/useLeaveGuard'
import { SECTION_IDS, buildBody, draftFromRaw, sectionOf, validate, type Draft, type SectionId } from '../components/pages/editorModel'
import type { SavedItem } from '../components/pages/EmbedBadges'
import type { PageSettings } from '../lib/pageSettings'
import { api, ApiError, errorText } from '../lib/api'
import { useLive } from '../lib/live'
import { stopCurrentAlarm } from '../lib/sounds'
import type { Channel, FieldErrors, PageBranding, PageLayout } from '../lib/types'

// Status page editor: the same rail + panel scaffold as Settings, with one
// sticky save bar. Creating a page is a short guided flow (CreateFlow) that
// lands here.

const META: Record<SectionId, { label: string; title: string; description: string; icon: ReactNode }> = {
  basics: { label: 'Basics', title: 'Basics', description: 'The page type, its name, address and description.', icon: <Icon.Info className="h-4 w-4" /> },
  services: { label: 'Services', title: 'Services', description: 'Choose which services and service groups the page shows, and in what order.', icon: <Icon.Server className="h-4 w-4" /> },
  appearance: { label: 'Appearance', title: 'Appearance', description: 'Logo, brand colour and theme, with a live preview.', icon: <Icon.Sun className="h-4 w-4" /> },
  content: { label: 'Content', title: 'Content', description: 'The description, the pinned announcement and the footer.', icon: <Icon.Edit className="h-4 w-4" /> },
  updates: { label: 'Updates & subscribers', title: 'Updates & subscribers', description: 'Maintenance, incident subscriptions, feeds and who is subscribed.', icon: <Icon.Bell className="h-4 w-4" /> },
  access: { label: 'Access', title: 'Access', description: 'Who can open the page, what visitors see, how it refreshes and how to embed it.', icon: <Icon.Lock className="h-4 w-4" /> },
}

export default function PageEditor({ id: idParam = 'new' }: { id?: string } = {}) {
  if (idParam === 'new') return <CreateFlow />
  return <EditPage id={Number(idParam)} />
}

interface Saved { slug: string; title: string; isPublic: boolean; canonical: string; layout: PageLayout; items: SavedItem[] }

function EditPage({ id }: { id: number }) {
  const [, navigate] = useLocation()
  const search = useSearch()
  const { push } = useToast()
  const live = useLive()
  const asked = new URLSearchParams(search).get('tab') as SectionId | null
  const section: SectionId = asked && SECTION_IDS.includes(asked) ? asked : 'basics'

  const [draft, setDraft] = useState<Draft | null>(null)
  const [baseline, setBaseline] = useState('')
  const [saved, setSaved] = useState<Saved | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'notfound' | 'error'>('loading')
  const [loadError, setLoadError] = useState('')
  const [nonce, setNonce] = useState(0)
  const [serverErrors, setServerErrors] = useState<FieldErrors>({})
  const [touched, setTouched] = useState<Set<string>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [emailChannels, setEmailChannels] = useState<Channel[]>([])
  const [channelError, setChannelError] = useState(false)

  const dirty = draft != null && baseline !== '' && JSON.stringify(draft) !== baseline
  const guard = useLeaveGuard(dirty, url => url.pathname === `/pages/${id}`)
  useEffect(() => () => stopCurrentAlarm(), [])

  useEffect(() => {
    api.get<{ channels: Channel[] }>('/api/channels')
      .then(d => setEmailChannels((d.channels ?? []).filter(c => c.type === 'email')))
      .catch(() => setChannelError(true))
  }, [])

  function apply(raw: unknown) {
    const d = draftFromRaw(raw)
    setDraft(d)
    setBaseline(JSON.stringify(d))
    setSaved({
      slug: d.slug, title: d.title, isPublic: d.visibility === 'public', canonical: d.settings.canonicalUrl, layout: d.layout,
      items: d.items.map(it => ({ groupId: it.groupId, monitorId: it.monitorId, displayName: it.displayName })),
    })
  }

  useEffect(() => {
    if (!Number.isFinite(id)) { setState('notfound'); return }
    let alive = true
    setState('loading')
    api.get<unknown>(`/api/pages/${id}`)
      .then(raw => { if (alive) { apply(raw); setState('ready') } })
      .catch(err => {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) setState('notfound')
        else { setLoadError(errorText(err, 'Could not load this page.')); setState('error') }
      })
    return () => { alive = false }
  }, [id, nonce])

  const live_ = useMemo(() => (draft ? validate(draft) : {}), [draft])
  const visible = (key: string): string | undefined => {
    if (serverErrors[key]) return serverErrors[key]
    const msg = live_[key]
    if (!msg || !draft) return undefined
    const empty = (key === 'title' && draft.title === '') || (key === 'slug' && draft.slug === '')
    return attempted || touched.has(key) || !empty ? msg : undefined
  }
  const errorKeys = [...new Set([...Object.keys(live_), ...Object.keys(serverErrors)])].filter(k => visible(k))
  const errorSections = new Set(errorKeys.map(sectionOf))

  const clearErr = (keys: string[]) => setServerErrors(e => { if (!keys.some(k => k in e)) return e; const n = { ...e }; for (const k of keys) delete n[k]; return n })
  const edit = (fn: (d: Draft) => Draft, keys: string[]) => { setDraft(d => (d ? fn(d) : d)); clearErr(keys) }

  const props: SectionProps | null = draft && {
    d: draft,
    patch: (p: Partial<Draft>) => edit(d => ({ ...d, ...p }), Object.keys(p)),
    brand: (p: Partial<PageBranding>) => edit(d => ({ ...d, branding: { ...d.branding, ...p } }), Object.keys(p)),
    set: (p: Partial<PageSettings>) => edit(d => ({ ...d, settings: { ...d.settings, ...p } }), Object.keys(p)),
    setBanner: p => edit(d => ({ ...d, settings: { ...d.settings, banner: { ...d.settings.banner, ...p } } }), ['bannerText', 'bannerSeverity']),
    setAudio: p => edit(d => ({ ...d, audio: { ...d.audio, ...p } }), []),
    err: visible,
    touch: key => setTouched(t => (t.has(key) ? t : new Set(t).add(key))),
  }

  async function save() {
    if (!draft) return
    setFormError(null)
    setAttempted(true)
    const fe = validate(draft)
    const first = Object.keys(fe)[0]
    if (first) {
      setFormError(`Review the highlighted fields (${Object.keys(fe).length}) before saving.`)
      if (sectionOf(first) !== section) navigate(`/pages/${id}?tab=${sectionOf(first)}`)
      return
    }
    setSaving(true)
    try {
      await api.put(`/api/pages/${id}`, buildBody(draft))
      push('Status page saved.', 'ok')
      setServerErrors({})
      setAttempted(false)
      apply(await api.get<unknown>(`/api/pages/${id}`))
    } catch (err) {
      if (err instanceof ApiError && err.fields && Object.keys(err.fields).length > 0) {
        setServerErrors(err.fields)
        setFormError('Fix the highlighted fields and save again.')
        const target = sectionOf(Object.keys(err.fields)[0])
        if (target !== section) navigate(`/pages/${id}?tab=${target}`)
      } else {
        setFormError(errorText(err, 'Could not save this page.'))
      }
    } finally {
      setSaving(false)
    }
  }

  async function doDelete() {
    try {
      await api.del(`/api/pages/${id}`)
      push('Status page deleted.', 'ok')
      navigate('/pages')
    } catch (err) {
      push(errorText(err, 'Could not delete this page.'), 'danger')
      setConfirmDelete(false)
    }
  }

  function discard() {
    if (!baseline) return
    setDraft(JSON.parse(baseline) as Draft)
    setServerErrors({})
    setAttempted(false)
    setFormError(null)
    setConfirmDiscard(false)
  }

  if (state === 'notfound') return <EmptyState heading title="Status page not found" action={<Button onClick={() => navigate('/pages')}>Back to status pages</Button>} />
  if (state === 'error') return <ErrorRetry message={loadError} onRetry={() => setNonce(n => n + 1)} />

  const ready = state === 'ready' && draft && props && saved
  const isBoard = draft?.layout === 'board'
  const railItem = (sid: SectionId) => ({
    id: sid, label: META[sid].label, href: `/pages/${id}?tab=${sid}`,
    icon: errorSections.has(sid) ? <Icon.Alert className="h-4 w-4 text-danger" /> : META[sid].icon,
    count: sid === 'services' && draft ? draft.items.length : undefined,
  })
  const groups: RailGroup[] = [
    { label: 'Page', items: [railItem('basics'), railItem('services')] },
    { label: 'Design', items: [railItem('appearance'), railItem('content')] },
    { label: 'Publishing', items: [railItem('updates'), railItem('access')] },
  ]
  const meta = META[section]
  const panelTitle = section === 'access' && isBoard ? 'Access & wallboard' : meta.title

  return (
    <>
      <div className="mb-3"><Crumbs items={[{ label: 'Status pages', href: '/pages' }, { label: draft?.title || 'Edit status page' }]} /></div>
      <PageTitle title={draft?.title || (ready ? 'Edit status page' : 'Status page')}
        actions={saved && <a href={`/status/${saved.slug}${saved.layout === 'board' ? '/board' : ''}`} target="_blank" rel="noopener noreferrer" className={buttonClass('secondary', 'md')}>View saved page<Icon.External className="h-3.5 w-3.5" /></a>}>
        Configure the page, its services and how updates reach visitors.
      </PageTitle>

      <SideNavLayout rail={<RailNav label="Status page sections" groups={groups} current={section} />}>
        <SettingsPanel key={section} id={`page-${section}`} title={panelTitle} description={meta.description}>
          {!ready ? <SettingsLoading label="Loading status page" sections={2} rows={3} /> : (
            <>
              {section === 'basics' && <BasicsSection {...props} onSlugEdited={() => undefined} />}
              {section === 'services' && <ServicesSection d={draft} patch={props.patch} err={props.err} groups={live.groups} monitors={live.monitors} connected={live.connected} />}
              {section === 'appearance' && <AppearanceSection {...props} />}
              {section === 'content' && <ContentSection {...props} />}
              {section === 'updates' && <UpdatesSection {...props} pageId={id} savedSlug={saved.slug} emailChannels={emailChannels} channelError={channelError} />}
              {section === 'access' && <AccessSection {...props} saved={{ ...saved, id }} monitors={live.monitors} />}
            </>
          )}
        </SettingsPanel>

        <div className="sticky bottom-[calc(76px+env(safe-area-inset-bottom))] z-10 mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[14px] border border-default bg-surface p-3 lg:bottom-3">
          <div className="flex min-h-9 min-w-0 flex-1 basis-56 items-center gap-3">
            {ready && <Button variant="ghost" size="sm" className="shrink-0 text-danger hover:bg-danger/10 hover:text-danger" onClick={() => setConfirmDelete(true)} icon={<Icon.Trash className="h-3.5 w-3.5" />}>Delete page</Button>}
            {ready ? (
              <span role="status" aria-live="polite" className={`flex min-w-0 items-center gap-1.5 text-xs ${formError ? 'text-danger' : 'text-muted'}`}>
                <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${formError ? 'bg-danger' : dirty ? 'bg-warn' : 'bg-ok'}`} />
                <span className="truncate">{formError ?? (dirty ? 'Unsaved changes' : 'All changes saved')}</span>
              </span>
            ) : <Skeleton className="h-4 w-40" />}
          </div>
          <Button variant="secondary" disabled={!ready || !dirty || saving} onClick={() => setConfirmDiscard(true)}>Discard</Button>
          <Button variant="primary" busy={saving} disabled={!ready || !dirty} onClick={save}>Save changes</Button>
        </div>
      </SideNavLayout>

      <ConfirmModal open={confirmDiscard} title="Discard unsaved changes?" description="Everything you changed since the last save is reset." confirmLabel="Discard changes" onConfirm={discard} onClose={() => setConfirmDiscard(false)} />
      <ConfirmModal open={guard.target !== null} title="Leave without saving?" description="Your changes to this status page have not been saved." confirmLabel="Leave page" onConfirm={guard.leave} onClose={guard.stay} />
      <ConfirmModal
        open={confirmDelete}
        title="Delete this status page?"
        description={<>Anyone with the link to <code className="font-mono">/status/{saved?.slug}</code> will get “Page not found”. Monitors are not affected.</>}
        confirmLabel="Delete page"
        onConfirm={doDelete}
        onClose={() => setConfirmDelete(false)}
      />
    </>
  )
}
