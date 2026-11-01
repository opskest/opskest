// Creating a status page: four short steps (name, address and type; theme;
// services; publish) that end on the editor, where everything else is one
// click away.

import { useEffect, useState, type ReactNode } from 'react'
import { useLocation } from 'wouter'
import { Crumbs, PageTitle } from '../PageShell'
import { Button, ConfirmModal, Icon, useToast } from '../ui'
import { SettingsFooter, SettingsPanel, SettingsSection } from '../settings/SettingsPanel'
import { AppearanceSection, IdentityFields, PageTypeCards, ServicesSection, ChoiceCard, pageTypeLabel } from './EditorSections'
import { EMPTY_DRAFT, buildBody, sectionOf, slugify, validate, type Draft } from './editorModel'
import { useLeaveGuard } from './useLeaveGuard'
import { api, ApiError, errorText } from '../../lib/api'
import { useLive } from '../../lib/live'
import type { PageSettings } from '../../lib/pageSettings'
import type { FieldErrors, PageBranding, PageVisibility } from '../../lib/types'

const STEPS = [
  { title: 'Name and type', description: 'What the page is called, where it lives and what kind of page it is.' },
  { title: 'Theme', description: 'Logo, colours and style, with a live preview. All optional, and everything can be changed later.' },
  { title: 'Services', description: 'Choose what the page shows. You can change this any time.' },
  { title: 'Publish', description: 'Check the page, choose who can open it, then create it.' },
] as const

const STEP_KEYS: Record<number, string[]> = {
  0: ['title', 'slug', 'description'],
  1: ['logoUrl', 'brandColor', 'websiteUrl', 'supportUrl', 'headerStyle', 'font', 'corners', 'colorMode'],
  2: ['items'],
}

export function CreateFlow() {
  const [, navigate] = useLocation()
  const { push } = useToast()
  const live = useLive()
  const [step, setStep] = useState(0)
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT)
  const [slugTouched, setSlugTouched] = useState(false)
  const [touched, setTouched] = useState<Set<string>>(new Set())
  const [attempted, setAttempted] = useState<Set<number>>(new Set())
  const [serverErrors, setServerErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const dirty = JSON.stringify(draft) !== JSON.stringify(EMPTY_DRAFT)
  const guard = useLeaveGuard(dirty, () => false)
  const all = validate(draft)

  const patch = (p: Partial<Draft>) => {
    setDraft(d => {
      const next = { ...d, ...p }
      if (p.title !== undefined && !slugTouched && p.slug === undefined) next.slug = slugify(p.title)
      return next
    })
    setServerErrors(e => { const n = { ...e }; for (const k of Object.keys(p)) delete n[k]; return n })
  }
  const brand = (p: Partial<PageBranding>) => setDraft(d => ({ ...d, branding: { ...d.branding, ...p } }))
  const set = (p: Partial<PageSettings>) => setDraft(d => ({ ...d, settings: { ...d.settings, ...p } }))
  const err = (key: string): string | undefined => {
    if (serverErrors[key]) return serverErrors[key]
    const msg = all[key]
    if (!msg) return undefined
    const empty = (key === 'title' && draft.title === '') || (key === 'slug' && draft.slug === '')
    return attempted.has(0) || touched.has(key) || !empty ? msg : undefined
  }

  useEffect(() => { setFormError(null) }, [step])

  function next() {
    setAttempted(a => new Set(a).add(step))
    const keys = STEP_KEYS[step] ?? []
    if (keys.some(k => all[k])) return
    setStep(s => s + 1)
  }

  async function create() {
    setBusy(true)
    setFormError(null)
    const fe = validate(draft)
    if (Object.keys(fe).length > 0) { setAttempted(new Set([0, 1, 2])); setStep(sectionStep(Object.keys(fe)[0])); setBusy(false); return }
    try {
      const res = await api.post<{ id?: number }>('/api/pages', buildBody(draft))
      let id = res?.id
      if (!id) {
        const list = await api.get<{ pages: { id: number; slug: string }[] }>('/api/pages')
        id = list.pages.find(p => p.slug === draft.slug)?.id
      }
      push(`Created /status/${draft.slug}. Customise it here.`, 'ok')
      navigate(id ? `/pages/${id}` : '/pages')
    } catch (e) {
      if (e instanceof ApiError && e.fields && Object.keys(e.fields).length > 0) {
        setServerErrors(e.fields)
        setStep(sectionStep(Object.keys(e.fields)[0]))
      } else setFormError(errorText(e, 'Could not create this page.'))
    } finally {
      setBusy(false)
    }
  }

  const meta = STEPS[step]
  const vis = (value: PageVisibility, title: string, hint: string, icon: ReactNode) => (
    <ChoiceCard name="visibility" value={value} checked={draft.visibility === value} title={title} hint={hint} icon={icon} onChange={() => patch({ visibility: value })} />
  )

  return (
    <>
      <div className="mb-3"><Crumbs items={[{ label: 'Status pages', href: '/pages' }, { label: 'New' }]} /></div>
      <PageTitle title="Create a status page">Four short steps. Content and delivery options come next, in the editor.</PageTitle>

      <div className="mx-auto w-full max-w-3xl">
        <SettingsPanel id="page-create" title={`Step ${step + 1} of ${STEPS.length}: ${meta.title}`} description={meta.description}>
          <div className="px-5 py-4">
            <ol aria-label="Steps" className="flex items-center gap-2 text-xs">
              {STEPS.map((s, i) => (
                <li key={s.title} aria-current={i === step ? 'step' : undefined} className="flex flex-1 items-center gap-2">
                  <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ${i < step ? 'bg-ok-bg text-ok' : i === step ? 'bg-accent text-accent-fg' : 'bg-surface-2 text-muted'}`}>
                    {i < step ? <Icon.Check className="h-3 w-3" /> : i + 1}
                  </span>
                  <span className={`hidden truncate sm:inline ${i === step ? 'font-semibold text-body' : 'text-muted'}`}>{s.title}</span>
                  {i < STEPS.length - 1 && <span aria-hidden="true" className={`h-px flex-1 ${i < step ? 'bg-ok' : 'bg-[var(--border)]'}`} />}
                </li>
              ))}
            </ol>
          </div>

          {step === 0 && (
            <SettingsSection>
              <PageTypeCards value={draft.layout} onChange={layout => patch({ layout })} />
              <p className="text-[12px] text-muted">Chosen now and fixed afterwards: a status page and a wallboard are separate things, so a page cannot be converted later.</p>
              <IdentityFields d={draft} patch={patch} err={err} touch={k => setTouched(t => new Set(t).add(k))} onSlugEdited={() => setSlugTouched(true)} />
            </SettingsSection>
          )}
          {step === 1 && (
            <AppearanceSection d={draft} patch={patch} brand={brand} set={set} err={err} touch={() => {}}
              setBanner={b => set({ banner: { ...draft.settings.banner, ...b } })} setAudio={a => patch({ audio: { ...draft.audio, ...a } })} />
          )}
          {step === 2 && (
            <ServicesSection d={draft} patch={patch} err={err} groups={live.groups} monitors={live.monitors} connected={live.connected} />
          )}
          {step === 3 && (
            <>
              <SettingsSection title="Summary">
                <dl className="grid gap-3 sm:grid-cols-2">
                  {[
                    ['Title', draft.title], ['Page type', pageTypeLabel(draft.layout)],
                    ['Address', `/status/${draft.slug}${draft.layout === 'board' ? '/board' : ''}`],
                    ['Theme', [draft.branding.brandColor ? `Brand ${draft.branding.brandColor}` : 'Default colours', draft.branding.logoUrl ? 'logo' : 'no logo'].join(' · ')],
                    ['Services', `${draft.items.filter(i => i.groupId != null).length} groups · ${draft.items.filter(i => i.monitorId != null).length} individual services`],
                  ].map(([k, v]) => (
                    <div key={k} className="min-w-0 rounded-[10px] border border-default bg-surface-2/40 px-4 py-3">
                      <dt className="text-[11px] font-semibold uppercase tracking-wider text-faint">{k}</dt>
                      <dd className="mt-1 truncate text-[13px] font-medium text-body">{v}</dd>
                    </div>
                  ))}
                </dl>
              </SettingsSection>
              <SettingsSection title="Who can open it" description="You can change this later under Access.">
                <div role="radiogroup" aria-label="Visibility" className="grid items-stretch gap-3 sm:grid-cols-2">
                  {vis('public', 'Public', 'Anyone with the link.', <Icon.Globe className="h-3.5 w-3.5" />)}
                  {vis('private', 'Private', 'Only signed-in Opskest users.', <Icon.Lock className="h-3.5 w-3.5" />)}
                </div>
              </SettingsSection>
            </>
          )}

          <div className="px-5 pb-5">
            <SettingsFooter status={formError ? <span className="text-danger">{formError}</span> : undefined}>
              <Button variant="secondary" onClick={() => guard.ask('/pages')}>Cancel</Button>
              {step > 0 && <Button variant="secondary" onClick={() => setStep(s => s - 1)}>Back</Button>}
              {step < STEPS.length - 1
                ? <Button variant="primary" onClick={next}>Continue</Button>
                : <Button variant="primary" busy={busy} onClick={create}>Create page</Button>}
            </SettingsFooter>
          </div>
        </SettingsPanel>
      </div>

      <ConfirmModal open={guard.target !== null} title="Discard this new page?" description="The page has not been created yet." confirmLabel="Discard" onConfirm={guard.leave} onClose={guard.stay} />
    </>
  )
}

/** The create step that holds a field. */
function sectionStep(key: string): number {
  const s = sectionOf(key)
  return s === 'basics' ? 0 : s === 'appearance' ? 1 : s === 'services' ? 2 : 3
}
