// HTTP monitor settings (spec §6). Up front: what makes a response count as
// healthy (keyword, JSON assertion) and domain expiry. Behind "Advanced":
// the request itself — method, accepted codes, redirects, headers, body,
// auth and TLS verification — which most monitors never touch.

import type { HeaderRow, SecretDraft, SecretKey, SettingsDraft } from '../../lib/monitorForm'
import type { FieldErrors } from '../../lib/types'
import { Input, Select, Switch, Textarea } from '../ui'
import { AssertionsEditor } from './AssertionsEditor'
import { HeadersEditor } from './HeadersEditor'
import { Disclosure, Field, NumberInput, SecretField, Subsection } from './primitives'

const METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']

const ADVANCED_KEYS = ['method', 'acceptedCodes', 'maxRedirects', 'bodyType', 'body', 'authType', 'basicUser', 'ignoreTls',
  'proxyUrl', 'proxyUser', 'caCert', 'oauthTokenUrl', 'oauthClientId', 'oauthScope', 'oauthAudience', 'oauthAuth']

export interface HttpFieldsProps {
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
}

function advancedSummary(s: SettingsDraft, headers: HeaderRow[], mtls: boolean): string {
  const parts = [s.method, s.acceptedCodes.trim() || '200-299']
  parts.push(s.maxRedirects === 0 ? 'no redirects' : `≤ ${Number.isFinite(s.maxRedirects) ? s.maxRedirects : '?'} redirects`)
  const n = headers.filter(h => h.stored || h.name.trim()).length
  if (n > 0) parts.push(`${n} header${n === 1 ? '' : 's'}`)
  if (s.bodyType !== 'none') parts.push(`${s.bodyType} body`)
  if (s.authType !== 'none') parts.push(s.authType === 'oauth2' ? 'OAuth2' : `${s.authType} auth`)
  if (s.proxyUrl.trim()) parts.push('proxy')
  if (mtls) parts.push('client certificate')
  if (s.caCert.trim()) parts.push('custom CA')
  if (s.ignoreTls) parts.push('TLS errors ignored')
  return parts.join(' · ')
}

export function HttpFields({
  settings: s, onSettings, errors, headers, onHeaders, basicPass, bearerToken, proxyPass, clientCert, clientKey, oauthSecret, onSecret,
}: HttpFieldsProps) {
  const e = (k: string) => errors[`settings.${k}`]
  const advancedError = ADVANCED_KEYS.some(k => e(k))
    || Object.keys(errors).some(k => /^(settings\.)?headers(\.|$)/.test(k) || k.startsWith('secrets.'))
  const mtls = clientCert.isSet || clientCert.value !== ''
  const advancedCustomised = s.method !== 'GET' || s.acceptedCodes.trim() !== '200-299' || s.maxRedirects !== 10
    || s.bodyType !== 'none' || s.authType !== 'none' || s.ignoreTls || headers.length > 0
    || s.proxyUrl.trim() !== '' || s.caCert.trim() !== '' || mtls

  return (
    <>
      <Subsection title="Keyword assertion" hint="Optional — also fail unless the response body passes this check.">
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Keyword" error={e('keyword')}>
            {id => <Input id={id} value={s.keyword} onChange={ev => onSettings({ keyword: ev.target.value })} placeholder="e.g. &quot;status&quot;:&quot;ok&quot;" aria-invalid={!!e('keyword')} className="font-mono" spellCheck={false} />}
          </Field>
          <Field label="Mode" error={e('keywordMode')}>
            {id => (
              <Select id={id} value={s.keywordMode} onChange={ev => onSettings({ keywordMode: ev.target.value as SettingsDraft['keywordMode'] })} disabled={!s.keyword}>
                <option value="contains">Body contains</option>
                <option value="not_contains">Body does not contain</option>
                <option value="regex">Body matches regex</option>
              </Select>
            )}
          </Field>
        </div>
        <Switch label="Case sensitive" checked={s.caseSensitive} disabled={!s.keyword} onChange={ev => onSettings({ caseSensitive: ev.target.checked })} />
      </Subsection>

      <Subsection title="JSON assertion" hint="Optional — read one value from a JSON response and compare it (gjson path syntax).">
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Path" error={e('jsonPath')}>
            {id => <Input id={id} value={s.jsonPath} onChange={ev => onSettings({ jsonPath: ev.target.value })} placeholder="data.status" className="font-mono" aria-invalid={!!e('jsonPath')} spellCheck={false} />}
          </Field>
          <Field label="Operator" error={e('jsonOp')}>
            {id => (
              <Select id={id} value={s.jsonOp} onChange={ev => onSettings({ jsonOp: ev.target.value as SettingsDraft['jsonOp'] })} disabled={!s.jsonPath} aria-invalid={!!e('jsonOp')}>
                <option value="==">equals</option>
                <option value="!=">does not equal</option>
                <option value="contains">contains</option>
                <option value=">">greater than</option>
                <option value="<">less than</option>
                <option value="exists">exists</option>
              </Select>
            )}
          </Field>
          <Field label="Expected value" error={e('jsonExpected')}>
            {id => (
              <Input id={id} value={s.jsonExpected} onChange={ev => onSettings({ jsonExpected: ev.target.value })} disabled={!s.jsonPath || s.jsonOp === 'exists'} placeholder={s.jsonOp === 'exists' ? '—' : 'ok'} className="font-mono" aria-invalid={!!e('jsonExpected')} spellCheck={false} />
            )}
          </Field>
        </div>
      </Subsection>

      <Subsection title="Domain">
        <Switch label="Check domain expiry daily (via RDAP) and warn before it lapses" checked={s.checkDomainExpiry} onChange={ev => onSettings({ checkDomainExpiry: ev.target.checked })} />
      </Subsection>

      <Subsection title="More checks">
        <AssertionsEditor value={s.assertions} onChange={assertions => onSettings({ assertions })} errors={errors} />
      </Subsection>

      <Subsection title="3rd-party status feed">
        <Switch
          label="This URL is a vendor's status page or feed (e.g. githubstatus.com): alert only when the vendor reports an incident, and don't count it as one of our monitors"
          checked={s.vendor}
          onChange={ev => onSettings({ vendor: ev.target.checked })}
        />
      </Subsection>

      <Disclosure title="Advanced request options" summary={advancedSummary(s, headers, mtls)} defaultOpen={advancedCustomised} forceOpen={advancedError}>
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Method" error={e('method')}>
            {id => (
              <Select id={id} value={s.method} onChange={ev => onSettings({ method: ev.target.value })} aria-invalid={!!e('method')}>
                {METHODS.map(m => <option key={m} value={m}>{m}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Accepted status codes" error={e('acceptedCodes')} hint="Codes and ranges, e.g. 200-299,301">
            {id => (
              <Input id={id} value={s.acceptedCodes} onChange={ev => onSettings({ acceptedCodes: ev.target.value })} aria-invalid={!!e('acceptedCodes')} className="font-mono" placeholder="200-299" />
            )}
          </Field>
          <Field label="Max redirects" error={e('maxRedirects')} hint="0 = don't follow; up to 20">
            {id => <NumberInput id={id} value={s.maxRedirects} onValue={n => onSettings({ maxRedirects: n })} min={0} max={20} aria-invalid={!!e('maxRedirects')} />}
          </Field>
          <div className="sm:pt-7">
            <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />
          </div>
        </div>

        <HeadersEditor rows={headers} onChange={onHeaders} errors={errors} idPrefix="http-headers" />

        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Request body" error={e('bodyType')}>
            {id => (
              <Select id={id} value={s.bodyType} onChange={ev => onSettings({ bodyType: ev.target.value as SettingsDraft['bodyType'] })}>
                <option value="none">None</option>
                <option value="json">JSON</option>
                <option value="form">Form (urlencoded)</option>
                <option value="raw">Raw text</option>
              </Select>
            )}
          </Field>
          <Field label="Authentication" error={e('authType')}>
            {id => (
              <Select id={id} value={s.authType} onChange={ev => onSettings({ authType: ev.target.value as SettingsDraft['authType'] })}>
                <option value="none">None</option>
                <option value="basic">Basic (user + password)</option>
                <option value="bearer">Bearer token</option>
                <option value="oauth2">OAuth2 client credentials</option>
              </Select>
            )}
          </Field>
        </div>

        {s.bodyType !== 'none' && (
          <Field label="Body" error={e('body')} hint="Up to 64 KiB.">
            {id => (
              <Textarea
                id={id}
                rows={4}
                value={s.body}
                onChange={ev => onSettings({ body: ev.target.value })}
                aria-invalid={!!e('body')}
                className="font-mono text-[12px]"
                placeholder={s.bodyType === 'json' ? '{"ping": true}' : s.bodyType === 'form' ? 'a=1&b=2' : ''}
                spellCheck={false}
              />
            )}
          </Field>
        )}

        {s.authType === 'basic' && (
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field label="User" error={e('basicUser')}>
              {id => <Input id={id} value={s.basicUser} onChange={ev => onSettings({ basicUser: ev.target.value })} autoComplete="off" aria-invalid={!!e('basicUser')} />}
            </Field>
            <SecretField label="Password" value={basicPass} onChange={v => onSecret('basicPass', v)} error={errors['secrets.basicPass']} />
          </div>
        )}
        {s.authType === 'bearer' && (
          <SecretField label="Token" value={bearerToken} onChange={v => onSecret('bearerToken', v)} error={errors['secrets.bearerToken']} hint="Sent as Authorization: Bearer <token>." />
        )}

        {s.authType === 'oauth2' && (
          <div className="flex flex-col gap-4 rounded-lg border border-default bg-surface-2/40 p-4">
            <p className="text-[12px] text-muted leading-relaxed">
              Fetches a token from the token endpoint with the client-credentials grant, keeps it until shortly before it
              expires, and sends it as <code className="font-mono">Authorization: Bearer</code>. A token the API rejects is discarded.
            </p>
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
              <Field label="Token URL" error={e('oauthTokenUrl')}>
                {id => <Input id={id} value={s.oauthTokenUrl} onChange={ev => onSettings({ oauthTokenUrl: ev.target.value })} placeholder="https://idp.example.com/oauth2/token" className="font-mono" aria-invalid={!!e('oauthTokenUrl')} spellCheck={false} />}
              </Field>
              <Field label="Client ID" error={e('oauthClientId')}>
                {id => <Input id={id} value={s.oauthClientId} onChange={ev => onSettings({ oauthClientId: ev.target.value })} autoComplete="off" aria-invalid={!!e('oauthClientId')} spellCheck={false} />}
              </Field>
              <SecretField label="Client secret" value={oauthSecret} onChange={v => onSecret('oauthSecret', v)} error={errors['secrets.oauthSecret']} />
              <Field label="Send credentials" error={e('oauthAuth')} hint="Most servers accept either; Basic is the standard.">
                {id => (
                  <Select id={id} value={s.oauthAuth} onChange={ev => onSettings({ oauthAuth: ev.target.value as SettingsDraft['oauthAuth'] })}>
                    <option value="basic">In an Authorization: Basic header</option>
                    <option value="post">In the request body</option>
                  </Select>
                )}
              </Field>
              <Field label="Scope" optional error={e('oauthScope')} hint="Space-separated.">
                {id => <Input id={id} value={s.oauthScope} onChange={ev => onSettings({ oauthScope: ev.target.value })} className="font-mono" spellCheck={false} />}
              </Field>
              <Field label="Audience" optional error={e('oauthAudience')} hint="Some providers (Auth0) need it.">
                {id => <Input id={id} value={s.oauthAudience} onChange={ev => onSettings({ oauthAudience: ev.target.value })} className="font-mono" spellCheck={false} />}
              </Field>
            </div>
          </div>
        )}

        <div className="flex flex-col gap-4 rounded-lg border border-default bg-surface-2/40 p-4">
          <div>
            <h4 className="text-sm font-semibold text-body">Proxy</h4>
            <p className="text-[12px] text-muted leading-relaxed">Send the request through an HTTP, HTTPS (CONNECT) or SOCKS5 proxy. Empty = connect directly.</p>
          </div>
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field label="Proxy URL" optional error={e('proxyUrl')} hint="Scheme, host and port only." className="sm:col-span-2">
              {id => <Input id={id} value={s.proxyUrl} onChange={ev => onSettings({ proxyUrl: ev.target.value })} placeholder="http://proxy.internal:3128 or socks5://10.0.0.5:1080" className="font-mono" aria-invalid={!!e('proxyUrl')} spellCheck={false} />}
            </Field>
            {s.proxyUrl.trim() !== '' && (
              <>
                <Field label="Proxy user" optional error={e('proxyUser')}>
                  {id => <Input id={id} value={s.proxyUser} onChange={ev => onSettings({ proxyUser: ev.target.value })} autoComplete="off" aria-invalid={!!e('proxyUser')} />}
                </Field>
                <SecretField label="Proxy password" value={proxyPass} onChange={v => onSecret('proxyPass', v)} error={errors['secrets.proxyPass']} />
              </>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-4 rounded-lg border border-default bg-surface-2/40 p-4">
          <div>
            <h4 className="text-sm font-semibold text-body">Certificates</h4>
            <p className="text-[12px] text-muted leading-relaxed">
              For servers that ask for a client certificate (mutual TLS) or use a private CA. The certificate and key are stored encrypted and never shown again.
            </p>
          </div>
          <Field label="Trusted CA certificates" optional error={e('caCert')} hint="PEM. Added to the system roots for this monitor only.">
            {id => <Textarea id={id} rows={4} value={s.caCert} onChange={ev => onSettings({ caCert: ev.target.value })} className="font-mono text-[12px]" placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} aria-invalid={!!e('caCert')} />}
          </Field>
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <SecretField multiline label="Client certificate" value={clientCert} onChange={v => onSecret('clientCert', v)} error={errors['secrets.clientCert']} placeholder="-----BEGIN CERTIFICATE-----" />
            <SecretField multiline label="Client private key" value={clientKey} onChange={v => onSecret('clientKey', v)} error={errors['secrets.clientKey']} placeholder="-----BEGIN PRIVATE KEY-----" />
          </div>
        </div>

      </Disclosure>
    </>
  )
}
