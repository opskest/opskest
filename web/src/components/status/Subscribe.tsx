// "Subscribe": how a visitor follows a public status page, in one dialog with
// three tabs. Email (double opt-in), RSS (the RSS 2.0 and Atom feed addresses,
// plus the JSON summary for developers) and Chat tools (Slack, Microsoft Teams
// and Google Workspace Chat, all reading the same feed address; Slack can also
// be given the visitor's own incoming webhook). Only what the page offers is
// shown. The dialog is the app's Modal: a full-width sheet on phones, Escape
// closes it and focus returns to the Subscribe button.

import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { api, ApiError, errorText } from '../../lib/api'
import { DEFAULT_SETTINGS, pageOrigin, type PageSettings } from '../../lib/pageSettings'
import type { PageBranding } from '../../lib/types'
import { Button, Icon, Input, Modal, Tabs } from '../ui'

type Kind = 'email' | 'rss' | 'chat'

/** A labelled read-only value with a Copy button; says "Copied" in words. */
function CopyField({ label, name, value }: { label?: string; name: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const id = useId()
  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch { /* clipboard blocked: the text is selectable to copy by hand */ }
  }
  return (
    <div className="flex flex-col gap-1.5">
      {label && <label htmlFor={id} className="text-[13px] font-medium text-body">{label}</label>}
      <div className="flex items-stretch gap-2">
        <input
          id={id}
          readOnly
          aria-label={name}
          value={value}
          onFocus={e => e.currentTarget.select()}
          className="min-w-0 flex-1 h-9 px-2.5 rounded-[10px] border border-default bg-ground text-[12px] font-mono text-body"
        />
        <Button size="sm" onClick={copy} aria-label={`Copy ${name}`} icon={copied ? <Icon.Check className="w-3.5 h-3.5 text-ok" /> : <Icon.Copy className="w-3.5 h-3.5" />}>
          <span aria-live="polite">{copied ? 'Copied' : 'Copy'}</span>
        </Button>
      </div>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="rounded-[14px] border border-default bg-surface-2/40 p-4 flex flex-col gap-2.5">
      <h3 className="text-[14px] font-semibold text-body">{title}</h3>
      {children}
    </section>
  )
}

const Hint = ({ children }: { children: ReactNode }) => <p className="text-[12px] text-muted leading-relaxed">{children}</p>

const tabLabel = (icon: ReactNode, text: string) => <span className="inline-flex items-center gap-1.5">{icon}{text}</span>

export function SubscribeMenu({ slug, branding, settings = DEFAULT_SETTINGS, feedAvailable = true }: { slug: string; branding?: PageBranding; settings?: PageSettings; feedAvailable?: boolean }) {
  const slackWebhook = !!branding?.subscribeSlack
  const tabs = [
    ...(branding?.subscribeEmail ? [{ key: 'email' as const, label: tabLabel(<Icon.Mail className="w-3.5 h-3.5" />, 'Email') }] : []),
    ...(feedAvailable ? [{ key: 'rss' as const, label: tabLabel(<Icon.Rss className="w-3.5 h-3.5" />, 'RSS') }] : []),
    ...(feedAvailable || slackWebhook ? [{ key: 'chat' as const, label: tabLabel(<Icon.Chat className="w-3.5 h-3.5" />, 'Chat tools') }] : []),
  ]
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<Kind>(tabs[0]?.key ?? 'rss')
  const [email, setEmail] = useState('')
  const [hook, setHook] = useState('')
  const [busy, setBusy] = useState<'email' | 'slack' | null>(null)
  const [note, setNote] = useState<{ ok: boolean; text: string; for: 'email' | 'slack' } | null>(null)
  const formId = useId()
  const origin = pageOrigin(settings)
  const api_ = `${origin}/api/public/pages/${encodeURIComponent(slug)}`
  const rss = `${api_}/feed.rss`
  const atom = `${api_}/feed.atom`
  const summary = `${api_}/summary.json`
  const active: Kind = tabs.some(t => t.key === tab) ? tab : (tabs[0]?.key ?? 'rss')

  function close() {
    setOpen(false)
    setNote(null)
    setEmail('')
    setHook('')
  }

  async function submit(e: FormEvent, kind: 'email' | 'slack') {
    e.preventDefault()
    setBusy(kind)
    setNote(null)
    try {
      const body = kind === 'email' ? { email: email.trim() } : { slackWebhookUrl: hook.trim() }
      const r = await api.post<{ message?: string }>(`/api/public/pages/${encodeURIComponent(slug)}/subscribe`, body)
      setNote({ ok: true, text: r.message ?? 'Subscribed.', for: kind })
      if (kind === 'email') setEmail(''); else setHook('')
    } catch (err) {
      setNote({ ok: false, text: err instanceof ApiError && err.status === 429 ? 'Too many attempts. Try again in a few minutes.' : errorText(err, 'Could not subscribe.'), for: kind })
    } finally {
      setBusy(null)
    }
  }

  const noteFor = (kind: 'email' | 'slack') => note && note.for === kind
    ? <p role="status" className={`text-[12px] leading-relaxed ${note.ok ? 'text-ok' : 'text-danger'}`}>{note.text}</p>
    : null

  const slackForm = (label: string) => (
    <form onSubmit={e => submit(e, 'slack')} className="flex flex-col gap-2">
      <label htmlFor={`${formId}-hook`} className="text-[13px] font-medium text-body">{label}</label>
      <div className="flex items-stretch gap-2">
        <Input id={`${formId}-hook`} type="url" required value={hook} onChange={e => setHook(e.target.value)} placeholder="https://hooks.slack.com/services/…" autoComplete="off" className="min-w-0 flex-1" />
        <Button type="submit" variant="primary" busy={busy === 'slack'}>Subscribe</Button>
      </div>
      {noteFor('slack')}
    </form>
  )

  return (
    <>
      <Button aria-label="Subscribe to status updates" aria-haspopup="dialog" onClick={() => setOpen(true)} className="max-sm:!px-0 max-sm:w-10" icon={<Icon.Bell className="w-4 h-4" />}>
        <span className="hidden sm:inline">Subscribe</span>
      </Button>
      <Modal
        open={open}
        onClose={close}
        title="Subscribe to updates"
        description="Choose how you want to hear about incidents on this page."
        footer={active === 'email'
          ? <Button type="submit" form={formId} variant="primary" busy={busy === 'email'}>{busy === 'email' ? 'Subscribing…' : 'Subscribe'}</Button>
          : undefined}
      >
        {tabs.length > 1 && <Tabs label="How to subscribe" tabs={tabs} active={active} onChange={k => { setTab(k); setNote(null) }} />}

        {active === 'email' && (
          <form id={formId} onSubmit={e => submit(e, 'email')} className="flex flex-col gap-2">
            <label htmlFor={`${formId}-email`} className="text-[13px] font-medium text-body">Enter your email address</label>
            <Input
              id={`${formId}-email`}
              type="email"
              required
              value={email}
              onChange={e => setEmail(e.target.value)}
              placeholder="name@example.com"
              autoComplete="email"
            />
            <Hint>You’ll receive emails for new incidents and updates to existing incidents.</Hint>
            {noteFor('email')}
          </form>
        )}

        {active === 'rss' && (
          <div className="flex flex-col gap-4">
            <CopyField label="Get the RSS feed" name="RSS feed URL" value={rss} />
            <CopyField label="Get the Atom feed" name="Atom feed URL" value={atom} />
            <Hint>Add either address to a feed reader. New incidents and their updates appear as they are posted.</Hint>
            <p className="border-t border-default pt-3 text-[12px] text-muted">
              Developers:{' '}
              <a href={summary} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-accent hover:underline">
                JSON summary (summary.json)<Icon.External className="w-3 h-3" />
              </a>
            </p>
          </div>
        )}

        {active === 'chat' && (
          <div className="flex flex-col gap-3">
            {feedAvailable && <Hint>All of these read the same feed address.</Hint>}
            {feedAvailable && (
              <Section title="Slack">
                <Hint>In a channel, run the command below. It needs Slack’s RSS app in your workspace.</Hint>
                <CopyField name="Slack command" value={`/feed subscribe ${rss}`} />
              </Section>
            )}
            {!feedAvailable && slackWebhook && <Section title="Slack">{slackForm('Post updates to your own Slack webhook')}</Section>}
            {feedAvailable && (
              <>
                <Section title="Microsoft Teams">
                  <Hint>Add the feed address to a channel with the Workflows app or the RSS app.</Hint>
                  <CopyField name="Microsoft Teams feed URL" value={rss} />
                </Section>
                <Section title="Google Workspace Chat">
                  <Hint>Use an RSS-to-chat bot or a webhook relay and give it the feed address.</Hint>
                  <CopyField name="Google Workspace Chat feed URL" value={rss} />
                </Section>
              </>
            )}
          </div>
        )}
      </Modal>
    </>
  )
}
