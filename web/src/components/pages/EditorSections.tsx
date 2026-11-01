// The bodies of the status page editor's sections. Each one returns
// SettingsSections for the shared Settings scaffold, so the editor looks and
// moves like Settings. State lives in the editor; these only render fields.

import type { ReactNode } from 'react'
import { Alert, Button, Field, Icon, Input, Select, Switch, Textarea } from '../ui'
import { SettingsSection } from '../settings/SettingsPanel'
import { Markdown } from '../status/Markdown'
import { FONTS, HEADER_STYLES, REFRESH_CHOICES, pageOrigin, type PageSettings } from '../../lib/pageSettings'
import type { Channel, Group, MonitorSummary, PageBranding, PageLayout, PageVisibility } from '../../lib/types'
import { playDownAlarm, playDegradedAlarm, stopCurrentAlarm, HEAVY_SOUND_OPTIONS, LIGHT_SOUND_OPTIONS, type HeavySoundType, type LightSoundType } from '../../lib/sounds'
import { EmbedBadges, type SavedItem } from './EmbedBadges'
import { PublicationItemsEditor } from './PublicationItemsEditor'
import { SubscribersPanel } from './SubscribersPanel'
import { BrandPreview } from './BrandPreview'
import { HEX_RE, type Draft } from './editorModel'

export interface SectionProps {
  d: Draft
  patch: (p: Partial<Draft>) => void
  brand: (p: Partial<PageBranding>) => void
  set: (p: Partial<PageSettings>) => void
  setBanner: (p: Partial<PageSettings['banner']>) => void
  setAudio: (p: Partial<Draft['audio']>) => void
  /** The message to show beside a field, if any. */
  err: (key: string) => string | undefined
  /** Mark a field as visited, so an empty required field starts to complain. */
  touch: (key: string) => void
}

export function pageTypeLabel(layout: PageLayout) { return layout === 'board' ? 'Wallboard' : 'Status page' }

export function ChoiceCard({ name, value, checked, title, hint, icon, onChange }: {
  name: string; value: string; checked: boolean; title: string; hint: string; icon: ReactNode; onChange: () => void
}) {
  return (
    <label className={`flex h-full cursor-pointer items-start gap-3 rounded-[10px] border p-3.5 transition-colors ${checked ? 'border-accent bg-accent-subtle' : 'border-default hover:border-strong'}`}>
      <input type="radio" name={name} value={value} checked={checked} onChange={onChange} className="mt-1 accent-accent" />
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="inline-flex items-center gap-1.5 text-[13px] font-semibold text-body">{icon}{title}</span>
        <span className="text-[12px] leading-relaxed text-muted">{hint}</span>
      </span>
    </label>
  )
}

export function PageTypeCards({ value, onChange }: { value: PageLayout; onChange: (v: PageLayout) => void }) {
  const opts: { value: PageLayout; title: string; hint: string; icon: ReactNode }[] = [
    { value: 'list', title: 'Status page', hint: 'Service history, incidents and subscriptions for your customers.', icon: <Icon.List className="h-4 w-4" /> },
    { value: 'board', title: 'Wallboard', hint: 'Live service health for a screen on the wall, with an optional alarm.', icon: <Icon.Tv className="h-4 w-4" /> },
  ]
  return (
    <div role="radiogroup" aria-label="Page type" className="grid items-stretch gap-3 sm:grid-cols-2">
      {opts.map(o => <ChoiceCard key={o.value} name="layout" value={o.value} checked={value === o.value} title={o.title} hint={o.hint} icon={o.icon} onChange={() => onChange(o.value)} />)}
    </div>
  )
}

/** Title, address and description of a page. Shared with the create flow. */
export function IdentityFields({ d, patch, err, touch, onSlugEdited }: Pick<SectionProps, 'd' | 'patch' | 'err' | 'touch'> & { onSlugEdited: () => void }) {
  const url = `${window.location.origin}/status/${d.slug || '…'}`
  return (
    <>
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Field label="Title" error={err('title')} hint="Shown at the top of the page.">
          {fid => <Input id={fid} value={d.title} onBlur={() => touch('title')} onChange={e => patch({ title: e.target.value })} placeholder="Acme service status" aria-invalid={!!err('title')} maxLength={200} />}
        </Field>
        <Field label="Page address" error={err('slug')} hint={<span className="break-all">{url}</span>}>
          {fid => (
            <Input id={fid} value={d.slug} onBlur={() => touch('slug')} onChange={e => { onSlugEdited(); patch({ slug: e.target.value.toLowerCase() }) }}
              placeholder="acme" className="font-mono" aria-invalid={!!err('slug')} maxLength={48} spellCheck={false} autoCapitalize="off" />
          )}
        </Field>
      </div>
      <Field label="Description" error={err('description')} hint="Shown under the title.">
        {fid => <Textarea id={fid} rows={2} value={d.description} onChange={e => patch({ description: e.target.value })} aria-invalid={!!err('description')} />}
      </Field>
    </>
  )
}

export function BasicsSection(p: SectionProps & { onSlugEdited: () => void }) {
  const board = p.d.layout === 'board'
  return (
    <>
      <SettingsSection title="Page type" description="Chosen when the page was created and fixed afterwards: a status page and a wallboard are separate things.">
        <div className="flex items-start gap-3 rounded-[10px] border border-default bg-surface-2/40 px-4 py-3">
          <span className="mt-0.5 text-accent">{board ? <Icon.Tv className="h-4 w-4" /> : <Icon.List className="h-4 w-4" />}</span>
          <div className="min-w-0">
            <p className="text-[13px] font-semibold text-body">{pageTypeLabel(p.d.layout)}</p>
            <p className="mt-0.5 text-[12px] leading-relaxed text-muted">Fixed when the page was created; it cannot be changed. To use the other type, create a new page.</p>
          </div>
        </div>
      </SettingsSection>
      <SettingsSection title="Details" description="What the page is called and where it lives.">
        <IdentityFields d={p.d} patch={p.patch} err={p.err} touch={p.touch} onSlugEdited={p.onSlugEdited} />
      </SettingsSection>
    </>
  )
}

export function ServicesSection({ d, patch, err, groups, monitors, connected }: Pick<SectionProps, 'd' | 'patch' | 'err'> & { groups: Group[]; monitors: Map<number, MonitorSummary>; connected: boolean }) {
  return (
    <SettingsSection title="Services and service groups" description="Service groups include all their services automatically. Set public names and descriptions, then arrange the order below.">
      {!connected && <Alert tone="warn">Waiting for the live service list. Existing selections are preserved.</Alert>}
      <PublicationItemsEditor items={d.items} onChange={items => patch({ items })} groups={groups} monitors={monitors} error={err('items')} />
    </SettingsSection>
  )
}

export function AppearanceSection({ d, brand, set, err }: SectionProps) {
  const st = d.settings
  const b = d.branding
  return (
    <>
      <SettingsSection title="Preview" description="Updates as you type. The real page keeps the app's spacing, contrast and focus rules in every combination.">
        <BrandPreview title={d.title} branding={b} settings={st} />
      </SettingsSection>
      <SettingsSection title="Brand identity" description="For customer-facing pages. Links must be https.">
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <Field label="Logo URL" error={err('logoUrl')} hint="Shown in the header and used as the favicon. SVG or PNG, ideally about 32px tall.">
            {fid => <Input id={fid} value={b.logoUrl} onChange={e => brand({ logoUrl: e.target.value })} placeholder="https://…/logo.svg" aria-invalid={!!err('logoUrl')} spellCheck={false} />}
          </Field>
          <Field label="Brand colour" error={err('brandColor')} hint="Accents and links only. Status colours stay standard.">
            {fid => (
              <div className="flex items-center gap-2">
                <input type="color" aria-label="Pick brand colour" value={HEX_RE.test(b.brandColor) ? b.brandColor : '#2563eb'}
                  onChange={e => brand({ brandColor: e.target.value })} className="h-10 w-12 shrink-0 cursor-pointer rounded-[10px] border border-default bg-ground p-1" />
                <Input id={fid} value={b.brandColor} onChange={e => brand({ brandColor: e.target.value })} placeholder="#2563eb" className="font-mono" aria-invalid={!!err('brandColor')} maxLength={7} />
                {b.brandColor && <Button variant="ghost" size="sm" onClick={() => brand({ brandColor: '' })}>Clear</Button>}
              </div>
            )}
          </Field>
          <Field label="Website link" error={err('websiteUrl')}>
            {fid => <Input id={fid} value={b.websiteUrl} onChange={e => brand({ websiteUrl: e.target.value })} placeholder="https://example.org" aria-invalid={!!err('websiteUrl')} spellCheck={false} />}
          </Field>
          <Field label="Support link" error={err('supportUrl')}>
            {fid => <Input id={fid} value={b.supportUrl} onChange={e => brand({ supportUrl: e.target.value })} placeholder="https://example.org/contact" aria-invalid={!!err('supportUrl')} spellCheck={false} />}
          </Field>
        </div>
      </SettingsSection>
      <SettingsSection title="Theme" description="Pick from safe options instead of custom CSS.">
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <Field label="Header style" error={err('headerStyle')} hint={(HEADER_STYLES.find(h => h.value === st.headerStyle)?.hint ?? '') + ' Uses the brand colour, if set.'}>
            {fid => (
              <Select id={fid} value={st.headerStyle} onChange={e => set({ headerStyle: e.target.value as PageSettings['headerStyle'] })}>
                {HEADER_STYLES.map(h => <option key={h.value} value={h.value}>{h.label}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Font" error={err('font')} hint="System fonts only, so nothing is downloaded from another site.">
            {fid => (
              <Select id={fid} value={st.font} onChange={e => set({ font: e.target.value as PageSettings['font'] })}>
                {FONTS.map(f => <option key={f.value} value={f.value}>{f.label}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Corners" error={err('corners')}>
            {fid => (
              <Select id={fid} value={st.corners} onChange={e => set({ corners: e.target.value as PageSettings['corners'] })}>
                <option value="rounded">Rounded</option>
                <option value="square">Square</option>
              </Select>
            )}
          </Field>
          <Field label="Appearance for new visitors" error={err('colorMode')} hint="Visitors can still pick light or dark themselves; their choice wins.">
            {fid => (
              <Select id={fid} value={st.colorMode} onChange={e => set({ colorMode: e.target.value as PageSettings['colorMode'] })}>
                <option value="">Opskest default (dark)</option>
                <option value="system">Follow the device</option>
                <option value="light">Light</option>
                <option value="dark">Dark</option>
              </Select>
            )}
          </Field>
        </div>
      </SettingsSection>
      {d.layout !== 'board' && (
        <SettingsSection title="Details on load" description="Service groups, incident updates and past days start collapsed so the page reads at a glance.">
          <Switch label="Open every group, timeline and section when the page loads" checked={st.defaultExpanded} onChange={e => set({ defaultExpanded: e.target.checked })}
            hint="Off: nothing is opened for the visitor, including a group that has a problem right now. A link to #group-name opens and scrolls to that group." />
        </SettingsSection>
      )}
    </>
  )
}

export function ContentSection({ d, set, setBanner, brand, err }: SectionProps) {
  const st = d.settings
  return (
    <>
      <SettingsSection title="Description" description="Optional copy for visitors. Markdown only: no HTML, links must be https, http or mailto, images https.">
        <Field label="Formatted description" error={err('descriptionMd')} hint="Replaces the plain description on the public page. Bold, italic, links, lists and short headings are supported.">
          {fid => <Textarea id={fid} rows={5} value={st.descriptionMd} onChange={e => set({ descriptionMd: e.target.value })} aria-invalid={!!err('descriptionMd')} maxLength={2000} spellCheck />}
        </Field>
        {st.descriptionMd.trim() && !err('descriptionMd') && <div className="rounded-[10px] border border-dashed border-default p-3 text-[13px] text-muted" aria-label="Description preview"><Markdown source={st.descriptionMd} /></div>}
      </SettingsSection>
      <SettingsSection title="Announcement" description="A pinned line above the services until you clear it, for example planned work or a known issue.">
        <div className="grid items-start gap-4 sm:grid-cols-[minmax(0,1fr)_200px]">
          <Field label="Pinned announcement" error={err('bannerText')} hint="One line.">
            {fid => <Input id={fid} value={st.banner.text} onChange={e => setBanner({ text: e.target.value })} aria-invalid={!!err('bannerText')} maxLength={300} placeholder="Payments are slower than usual while we upgrade." />}
          </Field>
          <Field label="Announcement style" error={err('bannerSeverity')}>
            {fid => (
              <Select id={fid} value={st.banner.severity} onChange={e => setBanner({ severity: e.target.value as PageSettings['banner']['severity'] })}>
                <option value="info">Information</option>
                <option value="success">Good news</option>
                <option value="warning">Notice</option>
                <option value="critical">Important</option>
              </Select>
            )}
          </Field>
        </div>
      </SettingsSection>
      <SettingsSection title="Footer" description="Shown at the bottom of every page. Markdown, same rules as the description.">
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <Field label="Footer text" error={err('footerText')} hint="Plain text, used when there is no formatted footer.">
            {fid => <Input id={fid} value={d.branding.footerText} onChange={e => brand({ footerText: e.target.value })} placeholder="© Opskest" aria-invalid={!!err('footerText')} maxLength={200} />}
          </Field>
          <Field label="Formatted footer" error={err('footerMd')} hint="Replaces the plain footer text when set.">
            {fid => <Textarea id={fid} rows={3} value={st.footerMd} onChange={e => set({ footerMd: e.target.value })} aria-invalid={!!err('footerMd')} maxLength={600} />}
          </Field>
        </div>
        {st.footerMd.trim() && !err('footerMd') && <div className="rounded-[10px] border border-dashed border-default p-3 text-center text-[13px] text-muted" aria-label="Footer preview"><Markdown source={st.footerMd} /></div>}
      </SettingsSection>
    </>
  )
}

export function UpdatesSection({ d, patch, set, err, pageId, savedSlug, emailChannels, channelError }: SectionProps & {
  pageId: number | null; savedSlug: string; emailChannels: Channel[]; channelError: boolean
}) {
  const st = d.settings
  const isPublic = d.visibility === 'public'
  return (
    <>
      <SettingsSection title="What the page shows" description="Windows and incidents that visitors can follow.">
        <Switch label="Show scheduled maintenance" checked={st.showMaintenance} onChange={e => set({ showMaintenance: e.target.checked })}
          hint="Upcoming and in-progress windows that affect this page's services." />
        {!st.showMaintenance && <p className="pl-12 text-[12px] leading-relaxed text-muted">Off: the public page, its summary, feeds and badges carry no maintenance. A service under maintenance reads as No data.</p>}
      </SettingsSection>
      <SettingsSection title="Incident subscriptions" description="Let visitors get incident updates by email (double opt-in) or in their own Slack channel. Public pages only; internal updates are never sent.">
        <Switch label="Let visitors subscribe to incident updates" checked={d.subscriptions.enabled} disabled={!isPublic}
          onChange={e => patch({ subscriptions: { ...d.subscriptions, enabled: e.target.checked } })} />
        {channelError && <Alert tone="warn">Email channels could not be loaded. Refresh the page before changing the email channel.</Alert>}
        {d.subscriptions.enabled && (
          <Field label="Send subscriber email through" error={err('subscriberChannelId')}
            hint={emailChannels.length === 0 ? 'No email channel yet. Add one in Settings, Alert delivery. Slack and RSS still work.' : 'Its SMTP server and From address are used. Leave empty to offer Slack and RSS only.'}>
            {fid => (
              <Select id={fid} value={d.subscriptions.channelId ?? ''} className="w-full sm:w-80" disabled={channelError}
                onChange={e => patch({ subscriptions: { ...d.subscriptions, channelId: e.target.value ? Number(e.target.value) : null } })}>
                <option value="">No email (Slack and RSS only)</option>
                {emailChannels.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
              </Select>
            )}
          </Field>
        )}
      </SettingsSection>
      <SettingsSection title="Feed and embedding" description="Machine-readable copies of the public page, for readers and widgets.">
        <Switch label="Offer the Atom feed of incidents" checked={st.feedEnabled} disabled={!isPublic} onChange={e => set({ feedEnabled: e.target.checked })}
          hint="Off: the feed answers not found and the Feed option disappears from the Subscribe menu." />
        {pageId != null && savedSlug && (
          <dl className="grid gap-3 text-[12px] sm:grid-cols-2">
            <div className="min-w-0"><dt className="text-muted">Status summary (JSON, Statuspage-compatible)</dt><dd className="mt-0.5"><code className="break-all font-mono text-body-2">{pageOrigin(st)}/api/public/pages/{savedSlug}/summary.json</code></dd></div>
            <div className="min-w-0"><dt className="text-muted">Incident feed (Atom)</dt><dd className="mt-0.5"><code className="break-all font-mono text-body-2">{pageOrigin(st)}/api/public/pages/{savedSlug}/feed.atom</code></dd></div>
          </dl>
        )}
      </SettingsSection>
      {pageId != null && <SettingsSection title="Subscribers" description="Everyone subscribed to this page."><SubscribersPanel pageId={pageId} /></SettingsSection>}
    </>
  )
}

function AudioSection({ d, setAudio }: { d: Draft; setAudio: SectionProps['setAudio'] }) {
  const a = d.audio
  const card = (tone: string, label: string, select: ReactNode, test: ReactNode) => (
    <div className="flex h-full flex-col gap-2 rounded-[10px] border border-default bg-surface-2/40 p-3.5">
      <span className="flex items-center gap-1.5 text-xs font-semibold text-body"><span className={`h-2 w-2 rounded-full ${tone}`} />{label}</span>
      {select}
      <div className="flex items-center gap-2 pt-1">{test}<Button type="button" variant="ghost" size="sm" onClick={() => stopCurrentAlarm()}>Stop</Button></div>
    </div>
  )
  return (
    <SettingsSection title="Wallboard audio" description="The board view plays a sound when a service on this page goes down (10 seconds) or degraded (5 seconds). Saved with the page, so every screen showing the board uses these settings. Viewers can still mute it on their own screen.">
      <Switch label="Play an alarm when a service goes down or becomes degraded" checked={a.enabled} onChange={e => setAudio({ enabled: e.target.checked })} />
      {a.enabled && (
        <div className="grid items-stretch gap-4 md:grid-cols-2">
          {card('bg-danger', 'Down (10 seconds)',
            <Select aria-label="Down sound" value={a.down} onChange={e => setAudio({ down: e.target.value as HeavySoundType })}>{HEAVY_SOUND_OPTIONS.map(o => <option key={o.id} value={o.id}>{o.label}</option>)}</Select>,
            <Button type="button" variant="secondary" size="sm" onClick={() => playDownAlarm(a.down, 10)}>Test down sound (10s)</Button>)}
          {card('bg-warn', 'Degraded (5 seconds)',
            <Select aria-label="Degraded sound" value={a.degraded} onChange={e => setAudio({ degraded: e.target.value as LightSoundType })}>{LIGHT_SOUND_OPTIONS.map(o => <option key={o.id} value={o.id}>{o.label}</option>)}</Select>,
            <Button type="button" variant="secondary" size="sm" onClick={() => playDegradedAlarm(a.degraded, 5)}>Test degraded sound (5s)</Button>)}
        </div>
      )}
      <p className="text-[12px] text-muted">Browsers only allow sound after someone clicks the page once. On a wallboard screen, click anywhere on the board after opening it.</p>
    </SettingsSection>
  )
}

export function AccessSection({ d, patch, brand, set, setAudio, err, saved, monitors }: SectionProps & {
  saved: { id: number | null; slug: string; title: string; isPublic: boolean; canonical: string; layout: PageLayout; items: SavedItem[] }
  monitors: Map<number, MonitorSummary>
}) {
  const st = d.settings
  const isBoard = d.layout === 'board'
  const vis = (value: PageVisibility, title: string, hint: string, icon: ReactNode) => (
    <ChoiceCard name="visibility" value={value} checked={d.visibility === value} title={title} hint={hint} icon={icon} onChange={() => patch({ visibility: value })} />
  )
  return (
    <>
      <SettingsSection title="Page access" description={d.visibility === 'public'
        ? 'Public pages show display names, statuses, uptime and incident text only: never targets, error reasons or runbooks.'
        : 'Private pages also show error reasons and response times.'}>
        <div role="radiogroup" aria-label="Visibility" className="grid items-stretch gap-3 sm:grid-cols-2">
          {vis('public', 'Public', 'Anyone with the link.', <Icon.Globe className="h-3.5 w-3.5" />)}
          {vis('private', 'Private', 'Only signed-in Opskest users.', <Icon.Lock className="h-3.5 w-3.5" />)}
        </div>
      </SettingsSection>
      <SettingsSection title="Page presentation" description={isBoard
        ? `This page is a wallboard, shown at /status/${d.slug || '…'}/board. Uptime history, response-time charts and visitor details belong to status pages.`
        : `This page is a status page, shown at /status/${d.slug || '…'}.`}>
        <Switch label={d.visibility === 'public' ? 'Show response times' : 'Show response times (always on for private pages)'}
          checked={d.showLatency || d.visibility === 'private'} disabled={d.visibility === 'private'} onChange={e => patch({ showLatency: e.target.checked })} />
        {!isBoard && (
          <div className="grid items-start gap-4 sm:grid-cols-2">
            <Field label="Uptime history" error={err('historyDays')}>
              {fid => (
                <Select id={fid} value={d.branding.historyDays} onChange={e => brand({ historyDays: Number(e.target.value) as 30 | 60 | 90 })}>
                  <option value={90}>90 days of bars</option>
                  <option value={60}>60 days of bars</option>
                  <option value={30}>30 days of bars</option>
                </Select>
              )}
            </Field>
            <div className="sm:pt-6">
              <Switch label="Show a response-time chart per component (p50 and p95)" checked={d.branding.showResponseTime} onChange={e => brand({ showResponseTime: e.target.checked })} />
            </div>
          </div>
        )}
      </SettingsSection>
      {!isBoard && (
        <SettingsSection title="What visitors see" description="Show or hide details on each service. Private tag names, targets and failure reasons are never shown on a public page, whatever you choose here.">
          <div className="grid gap-4 sm:grid-cols-3">
            <Switch label="Show uptime percentages" checked={st.showUptime} onChange={e => set({ showUptime: e.target.checked })} />
            <Switch label="Show when each service was last checked" checked={st.showLastCheck} onChange={e => set({ showLastCheck: e.target.checked })} />
            <Switch label="Show days until a service TLS certificate expires" checked={st.showCert} onChange={e => set({ showCert: e.target.checked })} />
          </div>
        </SettingsSection>
      )}
      <SettingsSection title="Refresh and address" description="How often the page re-reads incidents and maintenance, and the public address it should use behind your own domain.">
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <Field label="Auto-refresh" error={err('refreshSeconds')} hint="Live service status always streams; this covers incidents, maintenance and history while the tab is visible.">
            {fid => (
              <Select id={fid} value={st.refreshSeconds} onChange={e => set({ refreshSeconds: Number(e.target.value) as PageSettings['refreshSeconds'] })}>
                {REFRESH_CHOICES.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Public address (optional)" error={err('canonicalUrl')} hint="For a custom domain behind your reverse proxy, e.g. https://status.example.com. It does not set up DNS or TLS.">
            {fid => <Input id={fid} value={st.canonicalUrl} onChange={e => set({ canonicalUrl: e.target.value })} placeholder="https://status.example.com" aria-invalid={!!err('canonicalUrl')} spellCheck={false} className="font-mono" />}
          </Field>
        </div>
      </SettingsSection>
      {isBoard && <AudioSection d={d} setAudio={setAudio} />}
      <SettingsSection title="Embed" description="Status badges for READMEs, docs and websites. Public pages only; they show status and uptime, never targets or reasons.">
        <EmbedBadges slug={saved.slug} title={saved.title} items={saved.items} monitors={monitors} isNew={saved.id == null} savedPublic={saved.isPublic} origin={saved.canonical} layout={saved.layout} />
      </SettingsSection>
    </>
  )
}
