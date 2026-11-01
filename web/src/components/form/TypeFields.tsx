// The type-specific part of the monitor form (spec §6): HTTP delegates to
// HttpFields; TCP, Ping, DNS, Push, WebSocket and 3rd-party status (vendor
// feeds) are small enough to live here.

import { VENDOR_PROVIDERS, type HeaderRow, type SecretDraft, type SecretKey, type SettingsDraft } from '../../lib/monitorForm'
import type { DnsMatch, FieldErrors, MonitorType, VendorProvider } from '../../lib/types'
import { CopyRow } from '../detail/PushCard'
import { DnsExpectedField } from './DnsExpectedField'
import { Icon, Input, Select, Switch } from '../ui'
import { HeadersEditor } from './HeadersEditor'
import { GroupFields } from './GroupFields'
import { HttpFields } from './HttpFields'
import { SqlFields } from './SqlFields'
import { Field, NumberInput, SecretField } from './primitives'

export interface TypeFieldsProps {
  type: MonitorType
  settings: SettingsDraft
  onSettings: (patch: Partial<SettingsDraft>) => void
  errors: FieldErrors
  headers: HeaderRow[]
  onHeaders: (rows: HeaderRow[]) => void
  basicPass: SecretDraft
  bearerToken: SecretDraft
  proxyPass: SecretDraft
  clientCert: SecretDraft
  clientKey: SecretDraft
  oauthSecret: SecretDraft
  onSecret: (which: SecretKey, s: SecretDraft) => void
  /** Group: the monitor being edited, so it can't contain itself. */
  selfId?: number
  /** Edit only: the push monitor's URL. */
  pushUrl?: string
  /** Vendor: the provider was picked by hand (stop auto-detecting). */
  onProviderPicked?: () => void
  providerAuto?: boolean
  /** DNS: the hostname, for "Use current answers". */
  target?: string
}

export function TypeFields(props: TypeFieldsProps) {
  const { type, settings: s, onSettings, errors, headers, onHeaders, pushUrl } = props
  const e = (k: string) => errors[`settings.${k}`]

  switch (type) {
    case 'http':
      return <HttpFields {...props} />

    case 'tcp':
      return (
        <div className="flex flex-col gap-4">
          <Switch label="TLS — do a handshake, verify the certificate and track its expiry" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
          <div className="flex flex-col gap-3 pt-4 border-t border-default">
            <p className="text-[12px] text-muted leading-relaxed">
              Optional: after connecting, send something and require a reply. Leave both empty to only check that the port accepts connections.
              The reply is never shown in an alert.
            </p>
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
              <Field label="Send" optional error={e('send')} hint="Text, or hex bytes as hex:50494e470d0a.">
                {id => <Input id={id} value={s.send} onChange={ev => onSettings({ send: ev.target.value })} placeholder="PING" className="font-mono" aria-invalid={!!e('send')} spellCheck={false} />}
              </Field>
              <Field label="Expect in the reply" optional error={e('expect')} hint="Waits up to the timeout for it.">
                {id => <Input id={id} value={s.expect} onChange={ev => onSettings({ expect: ev.target.value })} placeholder={s.expectMode === 'regex' ? '^\\+PONG' : '+PONG'} className="font-mono" aria-invalid={!!e('expect')} spellCheck={false} />}
              </Field>
              <Field label="Match as" error={e('expectMode')}>
                {id => (
                  <Select id={id} value={s.expectMode} onChange={ev => onSettings({ expectMode: ev.target.value as SettingsDraft['expectMode'] })}>
                    <option value="contains">Text</option>
                    <option value="regex">Regular expression</option>
                  </Select>
                )}
              </Field>
            </div>
          </div>
        </div>
      )

    case 'group':
      return <GroupFields settings={s} onSettings={onSettings} errors={errors} selfId={props.selfId} />

    case 'manual':
      return (
        <div className="flex flex-col gap-3">
          <p className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed">
            <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
            Nothing is checked. It starts up; set it to degraded or down from the monitor&apos;s page when something happens, and back to up when it&apos;s
            over. Use it for work no check can see — a nightly job you watch by hand, a planned change.
          </p>
          <p className="text-[12px] text-muted leading-relaxed">
            From a script: <code className="font-mono">POST /api/monitors/&lt;id&gt;/status</code> with a JSON body like{' '}
            <code className="font-mono">{'{"status":"down","reason":"export failed"}'}</code> (signed in as an administrator).
          </p>
        </div>
      )

    case 'ping':
      return (
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Packets per check" error={e('count')} hint="1–10 echo requests each run.">
            {id => <NumberInput id={id} value={s.count} onValue={n => onSettings({ count: n })} min={1} max={10} unit="packets" aria-invalid={!!e('count')} />}
          </Field>
          <Field label="Max packet loss" error={e('maxLossPct')} hint="Down above this; 1–100.">
            {id => <NumberInput id={id} value={s.maxLossPct} onValue={n => onSettings({ maxLossPct: n })} min={1} max={100} unit="%" aria-invalid={!!e('maxLossPct')} />}
          </Field>
        </div>
      )

    case 'dns':
      return (
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Record" error={e('record')}>
            {id => (
              <Select id={id} value={s.record} onChange={ev => onSettings({ record: ev.target.value as SettingsDraft['record'] })}>
                {(['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS'] as const).map(r => <option key={r} value={r}>{r}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Resolver" error={e('resolver')} optional hint="Empty = the system resolver; otherwise ip[:port].">
            {id => <Input id={id} value={s.resolver} onChange={ev => onSettings({ resolver: ev.target.value })} placeholder="1.1.1.1" className="font-mono" aria-invalid={!!e('resolver')} spellCheck={false} />}
          </Field>
          <Field label="Match" error={e('match')}>
            {id => (
              <Select id={id} value={s.match} onChange={ev => onSettings({ match: ev.target.value as DnsMatch })} disabled={s.expectedValues.length === 0}>
                <option value="exact">Answers are exactly these (alert on any change)</option>
                <option value="all">Every value is among the answers</option>
                <option value="equals">Any answer equals one of these</option>
                <option value="contains">Any answer contains one of these</option>
              </Select>
            )}
          </Field>
          <DnsExpectedField
            target={props.target ?? ''}
            record={s.record}
            resolver={s.resolver}
            values={s.expectedValues}
            onValues={(values, match) => onSettings(match ? { expectedValues: values, match } : { expectedValues: values })}
            error={e('expectedValues')}
          />
        </div>
      )

    case 'push':
      return (
        <div className="flex flex-col gap-4">
          <Field label="Grace period" error={e('graceS')} hint="How late a push may be before the monitor fails; 0–3600." className="sm:w-[calc(50%-0.625rem)]">
            {id => <NumberInput id={id} value={s.graceS} onValue={n => onSettings({ graceS: n })} min={0} max={3600} unit="sec" aria-invalid={!!e('graceS')} />}
          </Field>
          {pushUrl
            ? (
              <div className="flex flex-col gap-1.5">
                <span className="text-[12px] font-medium text-body-2">Push URL</span>
                <CopyRow text={pushUrl} label="Push URL" />
                <p className="text-[12px] text-muted">GET or POST, optionally with ?status=up|down&amp;msg=…&amp;ms=…</p>
              </div>
            )
            : (
              <p className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed">
                <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
                The push URL is generated when you save — your job then calls it every interval.
              </p>
            )}
        </div>
      )

    case 'websocket':
      return (
        <div className="flex flex-col gap-5">
          <HeadersEditor rows={headers} onChange={onHeaders} errors={errors} idPrefix="ws-headers" />
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field label="Send after connecting" optional error={e('send')} hint="A message sent once the socket opens.">
              {id => <Input id={id} value={s.send} onChange={ev => onSettings({ send: ev.target.value })} className="font-mono" spellCheck={false} />}
            </Field>
            <Field label="Expect in the first reply" optional error={e('expect')} hint="A keyword the first reply must contain.">
              {id => <Input id={id} value={s.expect} onChange={ev => onSettings({ expect: ev.target.value })} className="font-mono" spellCheck={false} />}
            </Field>
          </div>
        </div>
      )

    case 'tls':
      return (
        <div className="flex flex-col gap-3">
          <p className="text-[12px] text-muted leading-relaxed">
            Opens a TLS connection, checks the certificate chain and name, and warns before it expires
            (see <em>Certificate warning</em> under timing). Port defaults to 443.
          </p>
          <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />
        </div>
      )

    case 'smtp':
      return (
        <div className="flex flex-col gap-3">
          <Field label="Security" error={e('security')} hint="How the connection is protected. Port 587 usually starts plain and upgrades with STARTTLS; 465 is TLS from the first byte." className="sm:w-[calc(50%-0.625rem)]">
            {id => (
              <Select id={id} value={s.security} onChange={ev => onSettings({ security: ev.target.value as SettingsDraft['security'] })}>
                <option value="">None — plain connection</option>
                <option value="starttls">STARTTLS — upgrade after greeting</option>
                <option value="tls">TLS — encrypted from the start</option>
              </Select>
            )}
          </Field>
          {(s.security === 'starttls' || s.security === 'tls') && (
            <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />
          )}
        </div>
      )

    case 'redis':
    case 'mqtt':
      return (
        <div className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field label="Username" optional hint={type === 'redis' ? 'Redis 6+ ACL user. Empty = the default user.' : 'Empty = connect anonymously.'}>
              {id => <Input id={id} value={s.basicUser} onChange={ev => onSettings({ basicUser: ev.target.value })} autoComplete="off" spellCheck={false} />}
            </Field>
            <SecretField label="Password" value={props.basicPass} onChange={v => props.onSecret('basicPass', v)} error={errors['secrets.basicPass']} />
          </div>
          <Switch label="TLS — encrypt the connection" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      )

    case 'postgres_sql':
    case 'mysql_sql':
      return <SqlFields type={type} settings={s} onSettings={onSettings} errors={errors} basicPass={props.basicPass} onPassword={v => props.onSecret('basicPass', v)} />

    case 'amqp':
      return (
        <div className="flex flex-col gap-3">
          <Switch label="TLS — encrypt the connection (amqps)" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      )

    case 'grpc':
      return (
        <div className="flex flex-col gap-4">
          <Field label="Service name" optional error={e('grpcService')} hint="Empty = the server overall. Otherwise the fully qualified service, e.g. orders.v1.Orders." className="sm:w-[calc(50%-0.625rem)]">
            {id => <Input id={id} value={s.grpcService} onChange={ev => onSettings({ grpcService: ev.target.value })} className="font-mono" spellCheck={false} />}
          </Field>
          <Switch label="TLS — use HTTPS (otherwise plain HTTP/2)" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      )

    case 'banner':
      return (
        <div className="flex flex-col gap-3">
          <Field label="First line must contain" error={e('expect')} hint="Checked against the first line the server sends, e.g. 220 for FTP, * OK for IMAP, +OK for POP3." className="sm:w-[calc(50%-0.625rem)]">
            {id => <Input id={id} value={s.expect} onChange={ev => onSettings({ expect: ev.target.value })} className="font-mono" spellCheck={false} aria-invalid={!!e('expect')} />}
          </Field>
          <Switch label="TLS — connect with TLS from the start (e.g. IMAPS 993)" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      )

    case 'mongodb':
      return (
        <div className="flex flex-col gap-3">
          <Switch label="TLS — encrypt the connection" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      )

    case 'ssh':
    case 'postgres':
    case 'mysql':
    case 'memcached':
      return (
        <p className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed">
          <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
          {type === 'ssh'
            ? 'Reads the SSH banner the server sends on connect. No login is attempted.'
            : type === 'memcached'
              ? 'Sends the version command and expects a VERSION reply.'
              : 'Checks the server answers the first step of the protocol. No login is attempted, so no credentials are needed and nothing appears in the database log as a failed sign-in.'}
        </p>
      )

    case 'ntp':
      return (
        <Field label="Max clock offset" error={e('maxOffsetMs')} optional hint="Down if this server's clock differs from the time server by more than this. 0 = only check that it answers." className="sm:w-[calc(50%-0.625rem)]">
          {id => <NumberInput id={id} value={s.maxOffsetMs} onValue={n => onSettings({ maxOffsetMs: n })} min={0} max={86400000} unit="ms" aria-invalid={!!e('maxOffsetMs')} />}
        </Field>
      )

    case 'udp':
      return (
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Send" optional error={e('send')} hint="Text, or hex bytes as hex:68656c6c6f.">
            {id => <Input id={id} value={s.send} onChange={ev => onSettings({ send: ev.target.value })} className="font-mono" spellCheck={false} />}
          </Field>
          <Field label="Expect in the reply" error={e('expect')} hint="Required — UDP has no handshake, so a matching reply is the only proof the service is alive.">
            {id => <Input id={id} value={s.expect} onChange={ev => onSettings({ expect: ev.target.value })} className="font-mono" spellCheck={false} aria-invalid={!!e('expect')} />}
          </Field>
        </div>
      )

    case 'vendor': {
      const provider = VENDOR_PROVIDERS.find(p => p.value === s.vendorProvider)
      return (
        <div className="flex flex-col gap-4">
          <p className="text-[12px] text-muted leading-relaxed">
            Reads the vendor&apos;s own status feed instead of pinging their status page, so it goes degraded or down only when
            the vendor reports an incident — and the alert carries the incident title.
          </p>
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field
              label="Provider"
              error={e('vendorProvider')}
              hint={props.providerAuto ? `Detected from the URL · ${provider?.hint ?? ''}` : provider?.hint}
            >
              {id => (
                <Select
                  id={id}
                  value={s.vendorProvider}
                  onChange={ev => { props.onProviderPicked?.(); onSettings({ vendorProvider: ev.target.value as VendorProvider }) }}
                  aria-invalid={!!e('vendorProvider')}
                >
                  {VENDOR_PROVIDERS.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
                </Select>
              )}
            </Field>
            <Field label="Only these components" optional error={e('vendorComponents')} hint="Comma-separated, e.g. API, Webhooks. Empty = every component.">
              {id => <Input id={id} value={s.vendorComponents} onChange={ev => onSettings({ vendorComponents: ev.target.value })} placeholder="API, Webhooks" aria-invalid={!!e('vendorComponents')} />}
            </Field>
          </div>
        </div>
      )
    }
  }
}
