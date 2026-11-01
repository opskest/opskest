import { useState, useEffect } from 'react'
import { useSearch } from 'wouter'
import { api, errorText, unwrapList } from '../lib/api'
import { dateServer } from '../lib/format'
import { serverTimeZone } from '../lib/runtime'
import type { Channel, ChannelType, AppSettings, User, AlertLogEntry, Me } from '../lib/types'
import { PageTitle } from '../components/PageShell'
import { Alert, Badge, Button, Checkbox, ConfirmModal, DropdownMenu, EmptyState, ErrorRetry, Field, Icon, Input, Modal, Select, SkeletonRows, Switch, Tooltip, useToast } from '../components/ui'
import { FieldGrid, Subsection } from '../components/form/primitives'
import { RailNav, SideNavLayout, type RailGroup } from '../components/RailNav'
import { RowActions, SettingsFooter, SettingsLoading, SettingsPanel, SettingsSection, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from '../components/settings/SettingsPanel'
import { LIST_HEAD, LIST_ROW, ListFrame } from '../components/table'
import { ChannelExtrasFields, defaultExtras, extrasFromConfig, extrasProblem, extrasToConfig, type ChannelExtras } from '../components/settings/ChannelExtras'
import { Pager, usePaging } from '../components/Pager'
import { BackupsPanel } from '../components/settings/BackupsPanel'
import { TagsPanel } from '../components/settings/TagsPanel'
import { ImportExportPanel } from '../components/settings/ImportExportPanel'
import { UsersPanel } from '../components/settings/UsersPanel'
import { DataPanel } from '../components/settings/DataPanel'
import { RegionsPanel } from '../components/settings/RegionsPanel'
import { AboutPanel } from '../components/settings/AboutPanel'
import { CHANNEL_LABELS } from '../lib/channels'

// The nightly backup hour is interpreted in the server's display timezone.

interface ChannelField { label: string; placeholder: string; hint?: string; required: boolean }

/** The fields of the chat and push channels: URL and key are secrets (blank keeps the stored value); target is plain. */
interface ChannelSpec { url?: ChannelField; key?: ChannelField; target?: ChannelField; user?: ChannelField; pass?: ChannelField; from?: ChannelField; to?: ChannelField; topic?: ChannelField }

const CHANNEL_SPEC: Partial<Record<ChannelType, ChannelSpec>> = {
  discord: { url: { label: 'Webhook URL', placeholder: 'https://discord.com/api/webhooks/…', hint: 'Channel settings → Integrations → Webhooks.', required: true } },
  mattermost: { url: { label: 'Incoming webhook URL', placeholder: 'https://mattermost.example.com/hooks/…', required: true } },
  rocketchat: { url: { label: 'Incoming webhook URL', placeholder: 'https://chat.example.com/hooks/…', required: true } },
  telegram: {
    key: { label: 'Bot token', placeholder: '123456:ABC-DEF…', hint: 'From @BotFather. The bot must be in the chat.', required: true },
    target: { label: 'Chat ID', placeholder: '-1001234567890', hint: 'A user, group or channel id.', required: true },
  },
  ntfy: {
    url: { label: 'Topic URL', placeholder: 'https://ntfy.sh/my-alerts', hint: 'Include the topic. Self-hosted servers work too.', required: true },
    key: { label: 'Access token', placeholder: 'tk_…', hint: 'Only for protected topics.', required: false },
  },
  gotify: {
    url: { label: 'Server URL', placeholder: 'https://gotify.example.com', required: true },
    key: { label: 'Application token', placeholder: 'A…', required: true },
  },
  pushover: {
    key: { label: 'Application token', placeholder: 'azGDORePK8gMaC0QOYAMyEEuzJnyUi', required: true },
    target: { label: 'User or group key', placeholder: 'uQiRzpo4DXghDmr9QzzfQu27cmVRsG', required: true },
  },
  matrix: {
    url: { label: 'Homeserver URL', placeholder: 'https://matrix.example.org', required: true },
    key: { label: 'Access token', placeholder: 'syt_…', required: true },
    target: { label: 'Room ID', placeholder: '!abcdef:example.org', hint: 'Not the alias: Room settings → Advanced.', required: true },
  },
  zulip: {
    url: { label: 'Zulip server', placeholder: 'https://yourorg.zulipchat.com', required: true },
    user: { label: 'Bot email', placeholder: 'alerts-bot@yourorg.zulipchat.com', hint: 'Settings → Bots → add a Generic bot.', required: true },
    key: { label: 'Bot API key', placeholder: 'abcd…', required: true },
    target: { label: 'Stream', placeholder: 'ops', hint: 'The bot must be subscribed to it.', required: true },
    topic: { label: 'Topic', placeholder: 'Opskest alerts', hint: 'Empty = Opskest alerts.', required: false },
  },
  webex: {
    key: { label: 'Bot access token', placeholder: 'NzI…', hint: 'Create a bot at developer.webex.com and add it to the space.', required: true },
    target: { label: 'Room ID', placeholder: 'Y2lzY29zcGFyazovL3VzL1JPT00v…', hint: 'The space the bot posts to.', required: true },
  },
  pushbullet: {
    key: { label: 'Access token', placeholder: 'o.…', hint: 'Account settings → Create access token.', required: true },
    target: { label: 'Device or email', placeholder: 'Empty = every device', hint: 'A device iden, or a contact email address.', required: false },
  },
  ifttt: {
    key: { label: 'Webhooks key', placeholder: 'dXXXXXXXXXXXXXXXXXXXXX', hint: 'From the Webhooks service page on ifttt.com.', required: true },
    target: { label: 'Event name', placeholder: 'opskest_alert', hint: 'Sent with value1 = headline, value2 = details, value3 = link.', required: true },
  },
  apprise: {
    url: { label: 'Apprise API server', placeholder: 'https://apprise.example.com', hint: 'A running apprise-api container. Reach 90+ services through it.', required: true },
    target: { label: 'Configuration key', placeholder: 'ops', hint: 'The key your notification URLs are saved under in Apprise.', required: true },
    user: { label: 'Username', placeholder: '', hint: 'Only if the server sits behind basic authentication.', required: false },
    pass: { label: 'Password', placeholder: '', required: false },
  },
  twilio: {
    user: { label: 'Account SID', placeholder: 'AC…', required: true },
    pass: { label: 'Auth token', placeholder: '', required: true },
    from: { label: 'From', placeholder: '+15551234567', hint: 'A Twilio number, or a messaging service SID (MG…).', required: true },
    to: { label: 'To numbers', placeholder: '+15557654321, +15559876543', hint: 'Comma-separated, up to 10, with country code. Each gets a short text.', required: true },
  },
  homeassistant: {
    url: { label: 'Home Assistant address', placeholder: 'http://homeassistant.local:8123', required: true },
    key: { label: 'Long-lived access token', placeholder: 'eyJ…', hint: 'Profile → Security → Long-lived access tokens.', required: true },
    target: { label: 'Notify service', placeholder: 'mobile_app_pixel', hint: 'The part after notify. in Developer tools → Actions.', required: true },
  },
  bark: {
    url: { label: 'Bark server', placeholder: 'https://api.day.app', hint: 'Empty = the public server. Set it for a self-hosted one.', required: false },
    key: { label: 'Device key', placeholder: 'XXXXXXXXXXXXXXXXXXXXXX', hint: 'Shown in the Bark app.', required: true },
  },
  signal: {
    url: { label: 'signal-cli REST API', placeholder: 'http://signal-api:8080', hint: 'A bbernhard/signal-cli-rest-api server with a registered number.', required: true },
    from: { label: 'Sender number', placeholder: '+15551234567', hint: 'The number registered with the server.', required: true },
    to: { label: 'Recipients', placeholder: '+15557654321', hint: 'Comma-separated, up to 10.', required: true },
    user: { label: 'Username', placeholder: '', hint: 'Only if the server sits behind basic authentication.', required: false },
    pass: { label: 'Password', placeholder: '', required: false },
  },
}

type SettingsSection = 'general' | 'notifications' | 'security' | 'regions' | 'database' | 'data' | 'import' | 'tags' | 'logs' | 'about'

// Panel titles are the rail labels, word for word; descriptions are one sentence ending in a period.
const PANEL_META: Record<SettingsSection, { title: string; description: string }> = {
  notifications: { title: 'Alert delivery', description: 'Where alerts are sent: chat, push, email, on-call and webhook channels.' },
  general: { title: 'Monitoring rules', description: 'Data retention, alert storm protection and reminder cadence.' },
  security: { title: 'Team access', description: 'Local accounts, roles and the trail of who changed what.' },
  regions: { title: 'Check regions', description: 'Check websites, APIs, ping and DNS from other countries.' },
  database: { title: 'Backups', description: 'Nightly schedule, manual snapshots and restore.' },
  data: { title: 'Storage', description: 'What the database holds, and how to clear old history to keep it small.' },
  tags: { title: 'Tags', description: 'Labels for monitors: create, rename, recolour and delete them.' },
  import: { title: 'Import & export', description: 'Move monitors in bulk: export JSON or CSV, or import a file or a list of URLs.' },
  logs: { title: 'Delivery history', description: 'Delivery attempts, retries and errors for every notification.' },
  about: { title: 'About & updates', description: 'Your version, new releases, install and roll back, and engine metrics.' },
}

// Alert delivery rows are four aligned columns on md and up: channel (icon, name, type) | details | status | actions.
const CHANNEL_COLS = 'md:grid-cols-[minmax(0,1.4fr)_minmax(0,1.6fr)_128px_172px]'

function channelIcon(type: string) {
  if (type === 'email') return Icon.Mail
  if (type === 'webhook' || type === 'apprise' || type === 'ifttt' || type === 'homeassistant') return Icon.Plug
  if (type === 'pagerduty' || type === 'opsgenie') return Icon.Alert
  if (type === 'pushover' || type === 'pushbullet' || type === 'ntfy' || type === 'gotify' || type === 'bark' || type === 'twilio' || type === 'signal') return Icon.Bell
  return Icon.Chat
}

const SECTIONS: SettingsSection[] = ['general', 'notifications', 'security', 'regions', 'database', 'data', 'import', 'tags', 'logs', 'about']

export default function SettingsPage({ user }: { user: Me }) {
  // Viewers only get Tools (read-only diagnostics); everything else is admin.
  const isAdmin = user.role === 'admin'
  const search = useSearch()
  const rawTab = new URLSearchParams(search).get('tab')
  // Updates used to be its own tab: old links land on About, which now holds it.
  const tab = (rawTab === 'updates' ? 'about' : rawTab) as SettingsSection | null
  const section: SettingsSection = tab && SECTIONS.includes(tab) ? tab : 'notifications'
  const [loading, setLoading] = useState(isAdmin)
  const [loadError, setLoadError] = useState('')

  // Channels State
  const [channels, setChannels] = useState<Channel[]>([])
  const [showChannelModal, setShowChannelModal] = useState(false)
  const [editingChannel, setEditingChannel] = useState<Channel | null>(null)
  const [channelName, setChannelName] = useState('')
  const [channelType, setChannelType] = useState<ChannelType>('slack')
  const [channelUrl, setChannelUrl] = useState('')
  const [channelKey, setChannelKey] = useState('')
  const [channelTarget, setChannelTarget] = useState('')
  const [emailHost, setEmailHost] = useState('')
  const [emailPort, setEmailPort] = useState(587)
  const [emailUser, setEmailUser] = useState('')
  const [emailPass, setEmailPass] = useState('')
  const [emailFrom, setEmailFrom] = useState('')
  const [emailTo, setEmailTo] = useState('')
  const [isDefaultChannel, setIsDefaultChannel] = useState(true)
  const [channelTopic, setChannelTopic] = useState('')
  const [extras, setExtras] = useState<ChannelExtras>(defaultExtras())
  const [applyAllConfirm, setApplyAllConfirm] = useState<{ id: number; name: string } | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [channelError, setChannelError] = useState<string | null>(null)
  const [channelDeleteConfirm, setChannelDeleteConfirm] = useState<{ id: number; name: string } | null>(null)

  // General Settings State
  const [settings, setSettings] = useState<AppSettings>({
    retentionRawDays: 7,
    stormThreshold: 5,
    stormWindowS: 30,
    backupHour: 3,
    backupKeep: 7,
    defaultReminderMin: 0,
    regionConfirm: 0,
    requireTwoFactor: false,
  })
  const [savingSettings, setSavingSettings] = useState(false)
  const [settingsSaved, setSettingsSaved] = useState(false)

  // Users State
  const [users, setUsers] = useState<User[]>([])

  // Logs State
  const [alertLogs, setAlertLogs] = useState<AlertLogEntry[]>([])
  const { push } = useToast()
  const [failedOnly, setFailedOnly] = useState(false)
  const [resending, setResending] = useState<number | null>(null)

  async function resendAlert(id: number) {
    setResending(id)
    try {
      await api.post(`/api/alert-log/${id}/resend`)
      push('Alert sent again', 'ok')
      loadAlertLogs()
    } catch (e) {
      push(errorText(e, 'Resend failed'), 'danger')
    } finally {
      setResending(null)
    }
  }
  const [logsLoading, setLogsLoading] = useState(false)
  // The server pages the delivery log by a `before` timestamp cursor; the
  // table pages what is loaded 25 at a time and "Load older" appends more.
  const [logsBefore, setLogsBefore] = useState<number | null>(null)
  const [logsLoadingOlder, setLogsLoadingOlder] = useState(false)
  const logPaging = usePaging(alertLogs, { resetKey: failedOnly })
  const channelPaging = usePaging(channels)

  // System Stats State
  const [systemStats, setSystemStats] = useState<any | null>(null)

  async function loadAll() {
    setLoading(true)
    setLoadError('')
    try {
      const [chRes, setRes, usRes, sysRes] = await Promise.all([
        api.get<{ channels: Channel[] }>('/api/channels'),
        api.get<AppSettings>('/api/settings'),
        api.get<{ users: User[] }>('/api/users'),
        api.get<any>('/api/system'),
      ])
      setChannels(chRes.channels || [])
      if (setRes) setSettings(setRes)
      setUsers(usRes.users || [])
      if (sysRes) setSystemStats(sysRes)
    } catch (err) {
      setLoadError(errorText(err, 'Settings could not load. Try again before making changes.'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (isAdmin) loadAll()
  }, [])

  async function loadAlertLogs() {
    setLogsLoading(true)
    try {
      const res = await api.get<unknown>(`/api/alert-log?limit=200${failedOnly ? '&failed=true' : ''}`)
      setAlertLogs(unwrapList<AlertLogEntry>(res, 'entries'))
      setLogsBefore(nextBeforeOf(res))
    } catch {
      setAlertLogs([])
      setLogsBefore(null)
    } finally {
      setLogsLoading(false)
    }
  }

  function nextBeforeOf(res: unknown): number | null {
    const nb = res && typeof res === 'object' ? (res as { nextBefore?: unknown }).nextBefore : null
    return typeof nb === 'number' ? nb : null
  }

  async function loadOlderAlertLogs() {
    if (logsBefore == null) return
    setLogsLoadingOlder(true)
    try {
      const res = await api.get<unknown>(`/api/alert-log?limit=200&before=${logsBefore}${failedOnly ? '&failed=true' : ''}`)
      const older = unwrapList<AlertLogEntry>(res, 'entries')
      setAlertLogs(cur => {
        const have = new Set(cur.map(l => l.id))
        return [...cur, ...older.filter(l => !have.has(l.id))]
      })
      setLogsBefore(nextBeforeOf(res))
    } catch (e) {
      push(errorText(e, 'Could not load older entries'), 'danger')
    } finally {
      setLogsLoadingOlder(false)
    }
  }

  useEffect(() => {
    if (section === 'logs') loadAlertLogs()
    if (section === 'about' || section === 'database') {
      api.get<any>('/api/system').then(res => setSystemStats(res)).catch(() => {})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section, failedOnly])

  function openCreateChannel() {
    setChannelError(null)
    setEditingChannel(null)
    setChannelName('')
    setChannelType('slack')
    setChannelUrl('')
    setChannelKey('')
    setChannelTarget('')
    setEmailHost('')
    setEmailPort(587)
    setEmailUser('')
    setEmailPass('')
    setEmailFrom('')
    setEmailTo('')
    setIsDefaultChannel(channels.length === 0)
    setChannelTopic('')
    setExtras(defaultExtras())
    setShowChannelModal(true)
  }

  function openEditChannel(ch: Channel) {
    setChannelError(null)
    setEditingChannel(ch)
    setChannelName(ch.name)
    setChannelType(ch.type)
    setChannelUrl('') // kept secret or blank unless updated
    setChannelKey('')
    setChannelTarget(ch.config.target || '')
    setEmailHost(ch.config.host || '')
    setEmailPort(ch.config.port || 587)
    setEmailUser(ch.config.username || '')
    setEmailPass('')
    setEmailFrom(ch.config.from || '')
    setEmailTo(ch.config.to ? ch.config.to.join(', ') : '')
    setIsDefaultChannel(ch.isDefault)
    setChannelTopic(ch.config.topic || '')
    setExtras(extrasFromConfig(ch.config))
    setShowChannelModal(true)
  }

  async function handleSaveChannel(e: React.FormEvent) {
    e.preventDefault()
    if (!channelName.trim()) return
    setChannelError(null)

    const config: any = {}
    if (channelType === 'slack' || channelType === 'teams' || channelType === 'gchat' || channelType === 'webhook') {
      if (channelUrl.trim()) {
        config.url = channelUrl.trim()
      } else if (!editingChannel) {
        setChannelError('Webhook URL is required.')
        return
      }
    } else if (channelType === 'pagerduty' || channelType === 'opsgenie') {
      if (channelKey.trim()) {
        // PagerDuty takes an integration (routing) key; Opsgenie an API key.
        if (channelType === 'opsgenie') config.apiKey = channelKey.trim()
        else config.routingKey = channelKey.trim()
      } else if (!editingChannel) {
        setChannelError('Integration / Routing Key is required.')
        return
      }
    } else if (CHANNEL_SPEC[channelType]) {
      const spec = CHANNEL_SPEC[channelType]!
      if (spec.url) {
        if (channelUrl.trim()) config.url = channelUrl.trim()
        else if (!editingChannel && spec.url.required) { setChannelError(`${spec.url.label} is required.`); return }
      }
      if (spec.key) {
        if (channelKey.trim()) config.apiKey = channelKey.trim()
        else if (!editingChannel && spec.key.required) { setChannelError(`${spec.key.label} is required.`); return }
      }
      if (spec.target) {
        if (!channelTarget.trim() && spec.target.required) { setChannelError(`${spec.target.label} is required.`); return }
        config.target = channelTarget.trim()
      }
      if (spec.user) {
        if (!emailUser.trim() && spec.user.required) { setChannelError(`${spec.user.label} is required.`); return }
        config.username = emailUser.trim()
      }
      if (spec.pass) {
        if (emailPass.trim()) config.password = emailPass.trim()
        else if (!editingChannel && spec.pass.required) { setChannelError(`${spec.pass.label} is required.`); return }
      }
      if (spec.from) {
        if (!emailFrom.trim() && spec.from.required) { setChannelError(`${spec.from.label} is required.`); return }
        config.from = emailFrom.trim()
      }
      if (spec.to) {
        config.to = emailTo.split(',').map(s => s.trim()).filter(Boolean)
        if (config.to.length === 0 && spec.to.required) { setChannelError(`${spec.to.label} is required.`); return }
      }
      if (spec.topic) config.topic = channelTopic.trim()
    } else if (channelType === 'email') {
      config.host = emailHost.trim()
      config.port = Number(emailPort)
      config.username = emailUser.trim()
      if (emailPass.trim()) config.password = emailPass.trim()
      config.from = emailFrom.trim()
      config.to = emailTo.split(',').map(s => s.trim()).filter(Boolean)
    }

    const extrasError = extrasProblem(extras)
    if (extrasError) { setChannelError(extrasError); return }
    Object.assign(config, extrasToConfig(extras))

    try {
      if (editingChannel) {
        await api.put(`/api/channels/${editingChannel.id}`, {
          name: channelName.trim(),
          type: channelType,
          isDefault: isDefaultChannel,
          config,
        })
      } else {
        await api.post('/api/channels', {
          name: channelName.trim(),
          type: channelType,
          isDefault: isDefaultChannel,
          config,
        })
      }
      setShowChannelModal(false)
      await loadAll()
    } catch (err: any) {
      setChannelError(err.message || 'Failed to save alert channel')
    }
  }

  async function handleTestChannel(id: number) {
    setTestingId(id)
    try {
      await api.post(`/api/channels/${id}/test`)
      push(`Test notification sent for channel #${id}.`, 'ok')
    } catch (err: any) {
      push(`Test failed: ${err.message}`, 'danger')
    } finally {
      setTestingId(null)
    }
  }

  async function handleApplyAll(id: number) {
    try {
      const r = await api.post<{ assigned?: number }>(`/api/channels/${id}/apply-all`)
      const n = r?.assigned ?? 0
      push(n === 0 ? 'Every monitor already sends to this channel.' : `Added to ${n} monitor${n === 1 ? '' : 's'}.`, 'ok')
    } catch (err: any) {
      push(err.message || 'Could not add the channel to every monitor', 'danger')
    } finally {
      setApplyAllConfirm(null)
    }
  }

  async function handleDeleteChannel(id: number) {
    try {
      await api.del(`/api/channels/${id}`)
      setChannelDeleteConfirm(null)
      await loadAll()
    } catch (err: any) {
      push(err.message || 'Failed to delete channel', 'danger')
    }
  }

  async function handleSaveSettings(e: React.FormEvent) {
    e.preventDefault()
    setSavingSettings(true)
    setSettingsSaved(false)
    try {
      // Merge onto the server's current values so fields owned by other
      // sections (multi-region) saved since this page loaded aren't undone.
      const current = await api.get<Record<string, unknown>>('/api/settings')
      const { regionConfirm: _rc, regionCountries: _rcs, regionBudget: _rb, ...mine } = settings as unknown as Record<string, unknown>
      void _rc; void _rcs; void _rb
      await api.put('/api/settings', { ...current, ...mine })
      setSettingsSaved(true)
      setTimeout(() => setSettingsSaved(false), 3500)
    } catch (err: any) {
      push(errorText(err, 'Failed to save settings'), 'danger')
    } finally {
      setSavingSettings(false)
    }
  }

  const railGroups: RailGroup[] = [
    { label: 'Workspace', items: [
      { id: 'notifications', label: 'Alert delivery', icon: <Icon.Bell className="h-4 w-4" />, count: loading ? null : channels.length },
      { id: 'general', label: 'Monitoring rules', icon: <Icon.Gear className="h-4 w-4" /> },
      { id: 'security', label: 'Team access', icon: <Icon.Shield className="h-4 w-4" />, count: loading ? null : users.length },
    ] },
    { label: 'Monitoring', items: [
      { id: 'regions', label: 'Check regions', icon: <Icon.Globe className="h-4 w-4" /> },
      { id: 'import', label: 'Import & export', icon: <Icon.Arrows className="h-4 w-4" /> },
      { id: 'tags', label: 'Tags', icon: <Icon.Tag className="h-4 w-4" /> },
    ] },
    { label: 'System', items: [
      { id: 'database', label: 'Backups', icon: <Icon.Database className="h-4 w-4" /> },
      { id: 'data', label: 'Storage', icon: <Icon.Server className="h-4 w-4" /> },
      { id: 'logs', label: 'Delivery history', icon: <Icon.List className="h-4 w-4" /> },
      { id: 'about', label: 'About & updates', icon: <Icon.Info className="h-4 w-4" /> },
    ] },
  ].map(g => ({ ...g, items: g.items.map(i => ({ ...i, href: `/settings?tab=${i.id}` })) }))

  const signInUsers = users.filter(u => u.role !== 'disabled')
  const twoFactorSummary = `${signInUsers.filter(u => u.twoFactorEnabled).length} of ${signInUsers.length} users have two-factor on`

  const meta = PANEL_META[section]
  // Tabs that read the settings loaded by loadAll() wait for it inside the panel, so the frame never changes.
  const dependsOnLoad = section === 'notifications' || section === 'general' || section === 'database'
  const gate = dependsOnLoad && (loadError
    ? <SettingsSection><ErrorRetry message={loadError} onRetry={loadAll} /></SettingsSection>
    : loading ? <SettingsLoading label="Loading settings" sections={section === 'general' ? 1 : 1} rows={section === 'general' ? 4 : 3} /> : null)

  const panelActions = section === 'notifications' ? (
    <Button variant="primary" onClick={openCreateChannel} icon={<Icon.Plus className="w-4 h-4" />}>Add alert channel</Button>
  ) : section === 'logs' ? (
    <>
      <Checkbox label="Failed only" checked={failedOnly} onChange={e => setFailedOnly(e.target.checked)} />
      <Button variant="secondary" onClick={loadAlertLogs} busy={logsLoading}>Refresh</Button>
    </>
  ) : undefined

  return (
    <>
      <PageTitle title="Workspace settings">
        Configure monitoring, manage team access, and maintain your installation.
      </PageTitle>

      <SideNavLayout rail={<RailNav label="Settings sections" groups={railGroups} current={section} />}>
        <SettingsPanel key={section} id={section} title={meta.title} description={meta.description} actions={panelActions}
          titleExtra={section === 'security' ? <Tooltip content="Channel webhook URLs, SMTP passwords and API secrets are encrypted at rest with AES-256-GCM."><span tabIndex={0} className="inline-flex rounded-full"><Badge tone="ok" dot>Encryption active</Badge></span></Tooltip> : undefined}>
          {/* 1. ALERT DELIVERY */}
          {section === 'notifications' && (gate || (channels.length === 0 ? (
            <SettingsSection>
              <EmptyState icon={<Icon.Bell className="w-5 h-5" />} title="No alert channels"
                action={<Button variant="secondary" onClick={openCreateChannel}>Add first channel</Button>}>
                Add a chat webhook, push service, or SMTP email to get notified when monitors fail.
              </EmptyState>
            </SettingsSection>
          ) : (
            <SettingsSection>
              <ListFrame as="ul" label="Alert channels">
                <li aria-hidden="true" className={`${LIST_HEAD} hidden ${CHANNEL_COLS} md:grid md:gap-x-4`}>
                  <span>Channel</span><span>Details</span><span>Status</span><span />
                </li>
                {channelPaging.rows.map(ch => {
                  const kind = CHANNEL_LABELS[ch.type]?.label ?? ch.type
                  const c = ch.config
                  const detail = [
                    c.from ? `From ${c.from}` : c.url?.isSet ? 'Webhook URL saved' : c.routingKey?.isSet ? 'Routing key saved' : c.apiKey?.isSet ? 'Access token saved' : c.target ? `Target ${c.target}` : 'Configured',
                    c.template ? 'Custom message' : '',
                  ].filter(Boolean).join(' · ')
                  const ChIcon = channelIcon(ch.type)
                  return (
                    <li key={ch.id} data-channel-row className={`${LIST_ROW} flex flex-wrap items-center gap-x-4 gap-y-2 md:grid ${CHANNEL_COLS}`}>
                      <div className="flex min-w-0 flex-1 basis-56 items-center gap-3 md:basis-auto">
                        <span aria-hidden="true" className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] bg-surface-2 text-muted"><ChIcon className="h-4 w-4" /></span>
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="truncate text-sm font-semibold text-body">{ch.name}</span>
                            {ch.isDefault && <Badge tone="accent">Default</Badge>}
                          </div>
                          <p className="truncate text-xs text-muted">{kind}</p>
                        </div>
                      </div>
                      <p className="min-w-0 basis-full truncate text-[13px] text-muted md:basis-auto" title={detail}>{detail}</p>
                      <div>
                        {c.quiet?.enabled
                          ? <Badge tone="warn" dot>Quiet {c.quiet.start}–{c.quiet.end}</Badge>
                          : <Badge tone="ok" dot>Active</Badge>}
                      </div>
                      <div className="ml-auto flex items-center justify-end gap-2 md:ml-0">
                        <Button variant="secondary" size="sm" busy={testingId === ch.id} onClick={() => handleTestChannel(ch.id)}>Test</Button>
                        <Button variant="secondary" size="sm" onClick={() => openEditChannel(ch)}>Edit</Button>
                        <DropdownMenu label={`More actions for ${ch.name}`} items={[
                          { key: 'all', label: 'Apply to all monitors', icon: <Icon.Bell className="h-3.5 w-3.5" />, onSelect: () => setApplyAllConfirm({ id: ch.id, name: ch.name }) },
                          { key: 'sep', separator: true },
                          { key: 'delete', label: 'Delete channel', icon: <Icon.Trash className="h-3.5 w-3.5" />, tone: 'danger', onSelect: () => setChannelDeleteConfirm({ id: ch.id, name: ch.name }) },
                        ]} />
                      </div>
                    </li>
                  )
                })}
              </ListFrame>
              <Pager paging={channelPaging} noun="Channels" />
            </SettingsSection>
          )))}

          {/* 2. MONITORING RULES */}
          {section === 'general' && (gate || (
            <SettingsSection>
              <form onSubmit={handleSaveSettings} className="space-y-5">
                <Subsection title="Retention and reminders" hint="How long raw results are kept and how often an open outage reminds you.">
                  <FieldGrid>
                    <Field label="Raw check retention (days)" hint="How long individual check results are kept before they are pruned (1–90).">
                      <Input type="number" min={1} max={90} value={settings.retentionRawDays}
                        onChange={e => setSettings({ ...settings, retentionRawDays: Number(e.target.value) })} required />
                    </Field>
                    <Field label="Reminder interval (minutes)" hint="Minutes between reminders while an outage is open. 0 notifies once.">
                      <Input type="number" min={0} max={1440} value={settings.defaultReminderMin}
                        onChange={e => setSettings({ ...settings, defaultReminderMin: Number(e.target.value) })} required />
                    </Field>
                  </FieldGrid>
                </Subsection>

                <Subsection title="Alert storm protection" hint="Holds notifications during widespread network or data center outages.">
                  <FieldGrid>
                    <Field label="Storm threshold (failures)" hint="Monitors failing at the same time before alerts are held (2–100).">
                      <Input type="number" min={2} max={100} value={settings.stormThreshold}
                        onChange={e => setSettings({ ...settings, stormThreshold: Number(e.target.value) })} required />
                    </Field>
                    <Field label="Storm window (seconds)" hint="The period in which those failures are counted (5–600).">
                      <Input type="number" min={5} max={600} value={settings.stormWindowS}
                        onChange={e => setSettings({ ...settings, stormWindowS: Number(e.target.value) })} required />
                    </Field>
                  </FieldGrid>
                </Subsection>

                <SettingsFooter status={settingsSaved ? <span className="flex items-center gap-1.5 font-semibold text-ok"><Icon.Check className="w-4 h-4" /> Settings saved</span> : undefined}>
                  <Button type="submit" variant="primary" busy={savingSettings}>Save settings</Button>
                </SettingsFooter>
              </form>
            </SettingsSection>
          ))}

          {/* 3. TEAM ACCESS */}
          {section === 'security' && <>
            <SettingsSection title="Two-factor authentication" description="Add a second step to every sign-in.">
              {loadError && <ErrorRetry message={loadError} onRetry={loadAll} />}
              <form onSubmit={handleSaveSettings}>
                <Switch label="Require two-factor authentication"
                  hint="Everyone must set up an authenticator app before they can use Opskest. Users without one are asked to set it up at their next page load."
                  checked={!!settings.requireTwoFactor} disabled={loading || !!loadError}
                  onChange={e => setSettings({ ...settings, requireTwoFactor: e.target.checked })} />
                <p role="status" className="mt-3 min-h-5 text-[13px] text-muted">{loading ? ' ' : twoFactorSummary}</p>
                {settings.requireTwoFactor && !user.twoFactorEnabled && <p className="mt-3 text-[13px] text-muted">You have not set up two-factor yet, so you will be asked to do it as soon as this is saved.</p>}
                <SettingsFooter status={settingsSaved ? <span className="flex items-center gap-1.5 font-semibold text-ok"><Icon.Check className="w-4 h-4" /> Settings saved</span> : undefined}>
                  <Button type="submit" variant="primary" busy={savingSettings} disabled={loading || !!loadError}>Save settings</Button>
                </SettingsFooter>
              </form>
            </SettingsSection>
            <UsersPanel me={user} onLoaded={setUsers} />
          </>}

          {/* 4. BACKUPS */}
          {section === 'database' && (gate || (
            <>
              <SettingsSection title="Nightly schedule" description="When the database snapshot is taken and how many are kept.">
                <form onSubmit={handleSaveSettings}>
                  <FieldGrid>
                    <Field label="Backup hour" hint={`Hour from 0–23 in ${serverTimeZone()} when the snapshot is taken.`}>
                      <Input type="number" min={0} max={23} value={settings.backupHour}
                        onChange={e => setSettings({ ...settings, backupHour: Number(e.target.value) })} required />
                    </Field>
                    <Field label="Snapshots to keep" hint="Older daily snapshots are deleted beyond this many (1–60).">
                      <Input type="number" min={1} max={60} value={settings.backupKeep}
                        onChange={e => setSettings({ ...settings, backupKeep: Number(e.target.value) })} required />
                    </Field>
                  </FieldGrid>
                  <SettingsFooter status={settingsSaved ? <span className="flex items-center gap-1.5 font-semibold text-ok"><Icon.Check className="w-4 h-4" /> Settings saved</span> : undefined}>
                    <Button type="submit" variant="primary" busy={savingSettings}>Save settings</Button>
                  </SettingsFooter>
                </form>
              </SettingsSection>
              <BackupsPanel />
            </>
          ))}

          {section === 'regions' && <RegionsPanel />}
          {section === 'data' && <DataPanel />}
          {section === 'import' && <ImportExportPanel />}
          {section === 'tags' && <TagsPanel />}

          {/* 5. DELIVERY HISTORY */}
          {section === 'logs' && (
            <SettingsSection>
              {!logsLoading && alertLogs.length === 0 ? (
                <EmptyState compact title={failedOnly ? 'No failed deliveries' : 'No deliveries yet'}>
                  {failedOnly ? 'Every recent notification was delivered. Turn off the filter to see them all.' : 'Notification attempts appear here as alerts are sent.'}
                </EmptyState>
              ) : (
                <SettingsTable scroll>
                  <table className={`${ST_TABLE} min-w-[820px]`}>
                    <thead className={ST_HEAD}>
                      <tr>
                        <th className={ST_TH}>Timestamp ({serverTimeZone()})</th>
                        <th className={ST_TH}>Channel</th>
                        <th className={ST_TH}>Monitor</th>
                        <th className={ST_TH}>Status</th>
                        <th className={ST_TH}>Event</th>
                        <th className={ST_TH}>Detail</th>
                        <th className={ST_TH}><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody className={ST_BODY}>
                      {logsLoading && alertLogs.length === 0 ? (
                        <tr><td colSpan={7} className="p-3"><SkeletonRows rows={4} framed={false} label="Loading delivery history" /></td></tr>
                      ) : (
                        logPaging.rows.map((l: any) => (
                          <tr key={l.id} className={ST_ROW}>
                            <td className={`${ST_TD} font-mono text-faint whitespace-nowrap`}>{dateServer(l.ts || l.createdAt)}</td>
                            <Tooltip onlyWhenTruncated content={l.channelName || null}><td className={`${ST_TD} font-semibold text-body whitespace-nowrap max-w-[10rem] truncate`}>{l.channelName || (l.channelId ? `Channel #${l.channelId}` : '—')}</td></Tooltip>
                            <Tooltip onlyWhenTruncated content={l.monitorName || null}><td className={`${ST_TD} text-muted whitespace-nowrap max-w-[10rem] truncate`}>{l.monitorName || (l.monitorId ? `#${l.monitorId}` : 'Test')}</td></Tooltip>
                            <td className={`${ST_TD} whitespace-nowrap`}>
                              <Badge tone={l.ok ? 'ok' : 'danger'} dot>{l.ok ? 'Delivered' : 'Failed'}</Badge>
                            </td>
                            <td className={`${ST_TD} font-mono text-xs text-muted whitespace-nowrap`}>{l.event || 'down'}</td>
                            <Tooltip onlyWhenTruncated content={`${l.error || (l.ok ? 'Delivered' : '')}${l.attempt > 1 ? ` · attempt ${l.attempt}` : ''}`}><td className={`${ST_TD} font-mono text-xs text-muted max-w-xs truncate whitespace-nowrap`}>
                              {l.error || (l.ok ? 'Delivered' : '—')}{l.attempt > 1 ? ` · attempt ${l.attempt}` : ''}
                            </td></Tooltip>
                            <td className={`${ST_TD} text-right whitespace-nowrap`}>
                              {!l.ok && l.monitorId && l.channelId && ['down', 'recovered', 'degraded', 'reminder'].includes(l.event) && (
                                <RowActions><Button size="sm" variant="secondary" busy={resending === l.id} onClick={() => resendAlert(l.id)}>Resend</Button></RowActions>
                              )}
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </SettingsTable>
              )}
              <Pager
                paging={logPaging}
                noun="Entries"
                hasMore={logsBefore != null}
                onLoadMore={loadOlderAlertLogs}
                loadingMore={logsLoadingOlder}
              />
            </SettingsSection>
          )}

          {/* 6. ABOUT & UPDATES */}
          {section === 'about' && <AboutPanel stats={systemStats} scrollToUpdates={rawTab === 'updates' || window.location.hash === '#updates'} />}
        </SettingsPanel>
      </SideNavLayout>

      {/* ── CREATE / EDIT CHANNEL MODAL ─────────────────────────────────────── */}
      {showChannelModal && (
        <Modal
          open={showChannelModal}
          title={editingChannel ? `Edit alert channel: ${editingChannel.name}` : 'Add alert channel'}
          onClose={() => setShowChannelModal(false)}
          size="lg"
          footerStart={<Switch
            label="Default channel for new monitors"
            checked={isDefaultChannel}
            onChange={e => setIsDefaultChannel(e.target.checked)}
          />}
          footer={<>
            <Button type="button" variant="secondary" onClick={() => setShowChannelModal(false)}>Cancel</Button>
            <Button type="submit" form="channel-form" variant="primary">{editingChannel ? 'Save changes' : 'Create channel'}</Button>
          </>}
        >
          <form id="channel-form" onSubmit={handleSaveChannel} className="space-y-5">
            <FieldGrid>
            <Field label="Channel name">
              <Input
                value={channelName}
                onChange={e => setChannelName(e.target.value)}
                placeholder="e.g. #ops-critical-alerts Slack"
                required
                autoFocus
              />
            </Field>
            <Field label="Channel type">
              <Select
                value={channelType}
                onChange={e => setChannelType(e.target.value as ChannelType)}
                disabled={!!editingChannel}
              >
                <option value="slack">Slack Incoming Webhook</option>
                <option value="email">Email (SMTP)</option>
                <option value="teams">Microsoft Teams Webhook</option>
                <option value="webhook">Custom HTTP Webhook</option>
                <option value="pagerduty">PagerDuty Events API v2</option>
                <option value="gchat">Google Chat Webhook</option>
                <option value="opsgenie">Opsgenie API</option>
                <optgroup label="Chat and push">
                  <option value="discord">Discord</option>
                  <option value="telegram">Telegram</option>
                  <option value="ntfy">ntfy</option>
                  <option value="gotify">Gotify</option>
                  <option value="pushover">Pushover</option>
                  <option value="matrix">Matrix</option>
                  <option value="mattermost">Mattermost</option>
                  <option value="rocketchat">Rocket.Chat</option>
                </optgroup>
                <optgroup label="More services">
                  <option value="zulip">Zulip</option>
                  <option value="webex">Webex</option>
                  <option value="pushbullet">Pushbullet</option>
                  <option value="ifttt">IFTTT</option>
                  <option value="apprise">Apprise API</option>
                  <option value="twilio">Twilio SMS</option>
                  <option value="homeassistant">Home Assistant</option>
                  <option value="bark">Bark</option>
                  <option value="signal">Signal</option>
                </optgroup>
              </Select>
            </Field>
            </FieldGrid>

            {(channelType === 'slack' || channelType === 'teams' || channelType === 'gchat' || channelType === 'webhook') && (
              <Field
                label="Webhook URL"
                hint={editingChannel ? 'Leave blank to retain current encrypted webhook URL' : 'Incoming webhook URL from your provider app'}
              >
                <Input
                  type="url"
                  value={channelUrl}
                  onChange={e => setChannelUrl(e.target.value)}
                  placeholder={editingChannel ? '•••••••••••••••••••• (Unchanged)' : 'https://hooks.slack.com/services/...'}
                  required={!editingChannel}
                />
              </Field>
            )}

            {(channelType === 'pagerduty' || channelType === 'opsgenie') && (
              <Field
                label="Routing / integration key"
                hint={editingChannel ? 'Leave blank to retain current key' : 'Integration secret key'}
              >
                <Input
                  value={channelKey}
                  onChange={e => setChannelKey(e.target.value)}
                  placeholder={editingChannel ? '•••••••••••••••••••• (Unchanged)' : 'Integration Key'}
                  required={!editingChannel}
                />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.url && (
              <Field label={CHANNEL_SPEC[channelType]!.url!.label} hint={editingChannel ? 'Leave blank to keep the current value' : CHANNEL_SPEC[channelType]!.url!.hint}>
                <Input
                  type="url"
                  value={channelUrl}
                  onChange={e => setChannelUrl(e.target.value)}
                  placeholder={editingChannel ? '•••••••••••••••••••• (Unchanged)' : CHANNEL_SPEC[channelType]!.url!.placeholder}
                  required={!editingChannel && CHANNEL_SPEC[channelType]!.url!.required}
                />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.key && (
              <Field label={CHANNEL_SPEC[channelType]!.key!.label} hint={editingChannel ? 'Leave blank to keep the current value' : CHANNEL_SPEC[channelType]!.key!.hint}>
                <Input
                  type="password"
                  autoComplete="off"
                  value={channelKey}
                  onChange={e => setChannelKey(e.target.value)}
                  placeholder={editingChannel ? '•••••••••••••••••••• (Unchanged)' : CHANNEL_SPEC[channelType]!.key!.placeholder}
                  required={!editingChannel && CHANNEL_SPEC[channelType]!.key!.required}
                />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.target && (
              <Field label={CHANNEL_SPEC[channelType]!.target!.label} hint={CHANNEL_SPEC[channelType]!.target!.hint}>
                <Input
                  value={channelTarget}
                  onChange={e => setChannelTarget(e.target.value)}
                  placeholder={CHANNEL_SPEC[channelType]!.target!.placeholder}
                  autoComplete="off"
                  spellCheck={false}
                  required={CHANNEL_SPEC[channelType]!.target!.required}
                />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.topic && (
              <Field label={CHANNEL_SPEC[channelType]!.topic!.label} hint={CHANNEL_SPEC[channelType]!.topic!.hint}>
                <Input value={channelTopic} onChange={e => setChannelTopic(e.target.value)} placeholder={CHANNEL_SPEC[channelType]!.topic!.placeholder} maxLength={60} />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.user && (
              <Field label={CHANNEL_SPEC[channelType]!.user!.label} hint={CHANNEL_SPEC[channelType]!.user!.hint}>
                <Input value={emailUser} onChange={e => setEmailUser(e.target.value)} placeholder={CHANNEL_SPEC[channelType]!.user!.placeholder} autoComplete="off" spellCheck={false} required={CHANNEL_SPEC[channelType]!.user!.required} />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.pass && (
              <Field label={CHANNEL_SPEC[channelType]!.pass!.label} hint={editingChannel ? 'Leave blank to keep the current value' : CHANNEL_SPEC[channelType]!.pass!.hint}>
                <Input type="password" autoComplete="new-password" value={emailPass} onChange={e => setEmailPass(e.target.value)} placeholder={editingChannel ? '•••••••••••••••••••• (Unchanged)' : CHANNEL_SPEC[channelType]!.pass!.placeholder} required={!editingChannel && CHANNEL_SPEC[channelType]!.pass!.required} />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.from && (
              <Field label={CHANNEL_SPEC[channelType]!.from!.label} hint={CHANNEL_SPEC[channelType]!.from!.hint}>
                <Input value={emailFrom} onChange={e => setEmailFrom(e.target.value)} placeholder={CHANNEL_SPEC[channelType]!.from!.placeholder} autoComplete="off" spellCheck={false} required={CHANNEL_SPEC[channelType]!.from!.required} />
              </Field>
            )}

            {CHANNEL_SPEC[channelType]?.to && (
              <Field label={CHANNEL_SPEC[channelType]!.to!.label} hint={CHANNEL_SPEC[channelType]!.to!.hint}>
                <Input value={emailTo} onChange={e => setEmailTo(e.target.value)} placeholder={CHANNEL_SPEC[channelType]!.to!.placeholder} autoComplete="off" spellCheck={false} required={CHANNEL_SPEC[channelType]!.to!.required} />
              </Field>
            )}

            {channelType === 'email' && (
              <div className="space-y-5">
                <FieldGrid>
                  <Field label="SMTP host">
                    <Input value={emailHost} onChange={e => setEmailHost(e.target.value)} placeholder="smtp.gmail.com" required />
                  </Field>
                  <Field label="SMTP port">
                    <Input type="number" value={emailPort} onChange={e => setEmailPort(Number(e.target.value))} required />
                  </Field>
                </FieldGrid>
                <FieldGrid>
                  <Field label="Username">
                    <Input value={emailUser} onChange={e => setEmailUser(e.target.value)} placeholder="alerts@example.com" />
                  </Field>
                  <Field label="Password" hint={editingChannel ? 'Leave blank to keep password' : 'App password'}>
                    <Input type="password" value={emailPass} onChange={e => setEmailPass(e.target.value)} placeholder="••••••••" />
                  </Field>
                </FieldGrid>
                <Field label="From address">
                  <Input value={emailFrom} onChange={e => setEmailFrom(e.target.value)} placeholder="noreply@example.com" required />
                </Field>
                <Field label="To recipients (comma-separated)">
                  <Input value={emailTo} onChange={e => setEmailTo(e.target.value)} placeholder="devops@example.com, oncall@example.com" required />
                </Field>
              </div>
            )}

            <ChannelExtrasFields value={extras} onChange={setExtras} zone={serverTimeZone()} />

            {channelError && <Alert tone="danger">{channelError}</Alert>}

          </form>
        </Modal>
      )}

      <ConfirmModal
        open={!!applyAllConfirm}
        title="Send every monitor's alerts to this channel?"
        description={applyAllConfirm ? `"${applyAllConfirm.name}" will be added to every existing monitor that does not use it yet, including paused ones. Monitors you add later still start from the default channels. You can take it off a selection of monitors from the monitor list.` : ''}
        onClose={() => setApplyAllConfirm(null)}
        onConfirm={() => { if (applyAllConfirm) return handleApplyAll(applyAllConfirm.id) }}
      />

      {/* ── DELETE CHANNEL CONFIRM MODAL ────────────────────────────────────── */}
      {channelDeleteConfirm && (
        <Modal
          open={!!channelDeleteConfirm}
          title="Delete alert channel?"
          onClose={() => setChannelDeleteConfirm(null)}
          footer={<>
            <Button type="button" variant="secondary" onClick={() => setChannelDeleteConfirm(null)}>Cancel</Button>
            <Button type="button" variant="danger" onClick={() => handleDeleteChannel(channelDeleteConfirm.id)}>Delete channel</Button>
          </>}
        >
          <div className="space-y-4">
            <p className="text-[13px] text-muted">
              Are you sure you want to delete <strong className="text-body font-semibold">"{channelDeleteConfirm.name}"</strong>?
              Monitors assigned to this channel will stop sending alerts to it.
            </p>
          </div>
        </Modal>
      )}
    </>
  )
}
