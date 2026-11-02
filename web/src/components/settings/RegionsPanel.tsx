import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'wouter'
import { api, errorText } from '../../lib/api'
import { COUNTRY_GROUPS, MAX_COUNTRIES, countryName, hostedCountry } from '../../lib/countries'
import { dateServer, duration, ms } from '../../lib/format'
import { serverRegion, serverTimeZone } from '../../lib/runtime'
import { useLive } from '../../lib/live'
import type { RegionCheck } from '../../lib/types'
import { RegionsBadge } from '../OutageTable'
import { Badge, Button, EmptyState, ErrorRetry, Field, Input, Select, Switch, Tooltip, useToast } from '../ui'
import { Pager, usePaging } from '../Pager'
import { FieldGrid } from '../form/primitives'
import { LIST_ROW, ListFrame } from '../table'
import { DANGER_GHOST, SettingsFooter, SettingsLoading, SettingsSection, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from './SettingsPanel'

// Settings → Check regions. This server checks every monitor on
// its interval; the rotation adds the rest of the world using the free
// Globalping probe network (nothing to host). Everything here is paced to
// Globalping's hourly allowance.

/** Each confirmation row can be tall (one chip per country), so pages start small. */
const RECENT_SIZES = [5, 10, 20] as const

interface Overview {
  enabled: boolean
  sweep: boolean
  alerts: boolean
  countries: string[]
  testsPerHour: number
  tokenSet: boolean
  usedHour: number
  eligible: number
  cycleS: number
  limit?: { limit: number; remaining: number; resetInS: number }
  recent: { outageId: number; monitorId: number; monitorName: string; startedAt: number; endedAt: number | null; regions: RegionCheck | null }[]
}

export function RegionsPanel() {
  const toast = useToast()
  const live = useLive()
  const [data, setData] = useState<Overview | null>(null)
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sweep, setSweep] = useState(true)
  const [alerts, setAlerts] = useState(true)
  const [enabled, setEnabled] = useState(false)
  const [countries, setCountries] = useState<string[]>([])
  const [perHour, setPerHour] = useState(225)
  const [saving, setSaving] = useState(false)
  const [token, setToken] = useState('')
  const [savingToken, setSavingToken] = useState(false)

  const [target, setTarget] = useState('')
  const [url, setUrl] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<{ target: string; result: RegionCheck } | null>(null)

  const load = useCallback(() => {
    setError(null)
    Promise.all([api.get<Overview>('/api/regions'), api.get<Record<string, unknown>>('/api/settings')])
      .then(([o, s]) => {
        setData(o); setSettings(s)
        setSweep(o.sweep); setAlerts(o.alerts); setEnabled(o.enabled); setCountries(o.countries); setPerHour(o.testsPerHour)
      })
      .catch(e => setError(errorText(e, 'Could not load region settings')))
  }, [])
  useEffect(load, [load])
  const hosted = hostedCountry(serverRegion(), serverTimeZone())
  const recentPaging = usePaging(data?.recent ?? [], { pageSize: RECENT_SIZES[0] })

  const monitors = useMemo(() => [...live.monitors.values()]
    .filter(m => !m.vendor && (m.type === 'http' || m.type === 'ping' || m.type === 'dns'))
    .sort((a, b) => a.name.localeCompare(b.name)), [live.monitors])

  const dirty = data != null && (sweep !== data.sweep || alerts !== data.alerts || enabled !== data.enabled ||
    perHour !== data.testsPerHour || countries.join() !== data.countries.join())

  // Rough rotation estimate for the values being edited.
  const cycleMin = data && data.eligible > 0
    ? Math.round((data.eligible * countries.length * 60) / Math.max(1, perHour - countries.length))
    : 0

  const toggle = (code: string) =>
    setCountries(c => c.includes(code) ? c.filter(x => x !== code) : c.length >= MAX_COUNTRIES ? c : [...c, code])

  async function save() {
    if (!settings) return
    if (countries.length === 0) { toast.push('Pick at least one country', 'danger'); return }
    setSaving(true)
    try {
      await api.put('/api/settings', {
        ...settings,
        regionSweep: sweep ? 1 : 0, regionAlerts: alerts ? 1 : 0, regionConfirm: enabled ? 1 : 0,
        regionCountries: countries.filter(c => c !== hosted), regionTestsPerHour: perHour,
      })
      toast.push('Region settings saved', 'ok')
      load()
    } catch (e) {
      toast.push(errorText(e, 'Could not save'), 'danger')
    } finally {
      setSaving(false)
    }
  }

  async function saveToken(value: string) {
    setSavingToken(true)
    try {
      await api.put('/api/regions/token', { token: value })
      toast.push(value ? 'Globalping token saved' : 'Globalping token removed', 'ok')
      setToken('')
      if (value && perHour < 450) setPerHour(450)
      load()
    } catch (e) {
      toast.push(errorText(e, 'Could not save the token'), 'danger')
    } finally {
      setSavingToken(false)
    }
  }

  async function runNow() {
    setRunning(true)
    setResult(null)
    try {
      setResult(await api.post<{ target: string; result: RegionCheck }>('/api/regions/check', target ? { monitorId: Number(target) } : { url }))
    } catch (e) {
      toast.push(errorText(e, 'Check failed'), 'danger')
    } finally {
      setRunning(false)
    }
  }

  if (error) return <SettingsSection><ErrorRetry message={error} onRetry={load} /></SettingsSection>
  if (!data) return <SettingsLoading label="Loading region settings" sections={3} rows={2} />
  const allowance = data.tokenSet ? 500 : 250

  return (
    <>
      <SettingsSection title="How it works">
        <p className="text-[13px] leading-relaxed text-body-2">
          Every check runs from <b>{serverRegion().toLowerCase() === 'this server' ? 'this server' : serverRegion()}</b> on its interval. Worldwide monitors are also checked from the countries below on a rotation (Globalping), and a country that fails while others succeed is re-checked once and alerted as a <b>regional issue</b>. Only the hostname and path are sent, never query strings, headers or credentials, and monitors with auth headers or private hostnames stay on this server.
        </p>
      </SettingsSection>

      <SettingsSection title="Worldwide checks" description={<>{data.eligible} monitors on the rotation · each checked from every country about every <b>{cycleMin ? duration(cycleMin * 60_000) : '—'}</b></>}>
        <FieldGrid>
          <div className="flex min-w-0 flex-col gap-4">
            <Switch label="Run worldwide checks" checked={sweep} onChange={e => setSweep(e.target.checked)}
              hint="Checks every Worldwide monitor from the countries below, on a rotation." />
            <Switch label="Alert on regional issues" checked={alerts} onChange={e => setAlerts(e.target.checked)}
              hint="Alerts when some countries fail while others succeed." />
            <Switch label="Also confirm every outage worldwide" checked={enabled} onChange={e => setEnabled(e.target.checked)}
              hint="Re-checks each new outage from all countries." />
          </div>
          <div className="flex min-w-0 flex-col gap-5">
            <Field label="Globalping checks per hour" hint={`One check = one country. Free allowance: ${allowance}/hour${data.tokenSet ? ' (with your token)' : ' — 500 with a free token'}. Leave headroom for “Check now”.`}>
              {id => <Input id={id} type="number" min={10} max={100000} value={perHour} onChange={e => setPerHour(Math.max(10, Number(e.target.value) || 10))} />}
            </Field>
            <div className="flex flex-col gap-1.5">
              <span className="text-[12px] font-medium text-body-2">Used in the last hour</span>
              <div className="h-2 overflow-hidden rounded-full bg-surface-2">
                <div className={`h-full ${data.usedHour >= data.testsPerHour ? 'bg-danger' : 'bg-accent'}`} style={{ width: `${Math.min(100, (data.usedHour / Math.max(1, data.testsPerHour)) * 100)}%` }} />
              </div>
              <span className="text-[12px] text-muted tnum">
                {data.usedHour} of {data.testsPerHour} by Opskest
                {data.limit && <> · Globalping says {data.limit.remaining} of {data.limit.limit} left, resets in {duration(data.limit.resetInS * 1000)}</>}
              </span>
            </div>
          </div>
        </FieldGrid>
      </SettingsSection>

      <SettingsSection title="Worldwide countries" description="Used by monitors set to Worldwide. Monitors can pick their own under “Monitoring region”."
        actions={<Badge tone={countries.filter(c => c !== hosted).length ? 'accent' : 'danger'}>{countries.filter(c => c !== hosted).length} / {MAX_COUNTRIES} selected</Badge>}
        footer={<SettingsFooter status={dirty ? <span className="text-warn">Unsaved changes</span> : undefined}>
          {dirty && <Button variant="ghost" onClick={() => { setSweep(data.sweep); setAlerts(data.alerts); setEnabled(data.enabled); setCountries(data.countries); setPerHour(data.testsPerHour) }}>Discard</Button>}
          <Button variant="primary" busy={saving} disabled={!dirty} onClick={save}>Save</Button>
        </SettingsFooter>}>
        {COUNTRY_GROUPS.map(g => (
          <div key={g.region}>
            <div className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-faint">{g.region}</div>
            <div className="flex flex-wrap gap-2">
              {g.list.map(([code, name]) => {
                const isHosted = code === hosted
                const on = !isHosted && countries.includes(code)
                const full = !on && !isHosted && countries.filter(c => c !== hosted).length >= MAX_COUNTRIES
                if (isHosted) {
                  // Opskest already checks every monitor from here: dimmed, not selectable, and it says why.
                  const why = `Hosted country: every check already runs from ${name}, so it is not part of the worldwide rotation.`
                  return (
                    <Tooltip key={code} content={why}>
                      <button type="button" aria-disabled="true" aria-label={`${name}: ${why}`} onClick={() => toast.push(why, 'neutral')}
                        className="inline-flex h-8 cursor-not-allowed items-center gap-2 rounded-full border border-default px-3 text-[12px] text-muted opacity-40">
                        <span className="font-mono text-[11px] font-semibold">{code}</span>{name}
                      </button>
                    </Tooltip>
                  )
                }
                return (
                  <button key={code} type="button" onClick={() => toggle(code)} disabled={full} aria-pressed={on}
                    className={`inline-flex h-8 items-center gap-2 rounded-full border px-3 text-[12px] transition-colors ${on ? 'border-accent bg-accent-subtle text-body' : 'border-default text-muted hover:text-body hover:border-strong'} ${full ? 'opacity-40 cursor-not-allowed' : ''}`}>
                    <span className="font-mono text-[11px] font-semibold">{code}</span>{name}
                  </button>
                )
              })}
            </div>
          </div>
        ))}
      </SettingsSection>

      <SettingsSection title="Globalping token (optional)" description="A free Globalping account doubles the allowance to 500 checks/hour, so every monitor is checked worldwide twice as often. Create one at globalping.io → Tokens. Stored encrypted; never shown again.">
        <div className="flex flex-wrap items-center gap-3">
          <Badge tone={data.tokenSet ? 'ok' : 'neutral'} dot>{data.tokenSet ? 'Token set' : 'No token'}</Badge>
          <Input className="flex-1 min-w-[14rem] font-mono" type="password" autoComplete="off" placeholder={data.tokenSet ? 'Paste a new token to replace it' : 'Paste your Globalping token'} value={token} onChange={e => setToken(e.target.value)} />
          <Button variant="primary" busy={savingToken} disabled={!token.trim()} onClick={() => saveToken(token.trim())}>Save token</Button>
          {data.tokenSet && <Button variant="ghost" className={DANGER_GHOST} onClick={() => saveToken('')}>Remove</Button>}
        </div>
      </SettingsSection>

      <SettingsSection title="Check now" description="Check any public URL from the worldwide countries right away. (A monitor's own page has “Check worldwide now”.)">
        <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] items-end">
          <Field label="Monitor">
            {id => (
              <Select id={id} value={target} onChange={e => setTarget(e.target.value)}>
                <option value="">— or enter a URL —</option>
                {monitors.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
              </Select>
            )}
          </Field>
          <Field label="URL">
            {id => <Input id={id} value={url} disabled={target !== ''} placeholder="https://www.example.com" onChange={e => setUrl(e.target.value)} />}
          </Field>
          <Button variant="primary" busy={running} disabled={!target && !url.trim()} onClick={runNow}>Check now</Button>
        </div>
        {result && (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2 text-[12px]">
              {result.result.total > 0 && !result.result.error
                ? <Badge tone={result.result.reachable === result.result.total ? 'ok' : result.result.reachable === 0 ? 'danger' : 'warn'}>
                    {result.result.reachable === result.result.total ? `Reachable from all ${result.result.total}` : `Reachable from ${result.result.reachable} of ${result.result.total}`}
                  </Badge>
                : <RegionsBadge rc={result.result} />}
              <span className="font-mono text-muted truncate">{result.target}</span>
              {result.result.error && <span className="text-danger">{result.result.error}</span>}
            </div>
            {(result.result.probes ?? []).length === 0 ? (
              <EmptyState compact title="No probe answered" />
            ) : (
            <SettingsTable>
              <table className={`${ST_TABLE} min-w-[560px]`}>
                <thead className={ST_HEAD}>
                  <tr><th className={ST_TH}>Country</th><th className={ST_TH}>City</th><th className={ST_TH}>Result</th><th className={`${ST_TH} text-right`}>Code</th><th className={`${ST_TH} text-right`}>Time</th><th className={ST_TH}>Detail</th></tr>
                </thead>
                <tbody className={ST_BODY}>
                  {(result.result.probes ?? []).map((p, i) => (
                    <tr key={i} className={ST_ROW}>
                      <td className={ST_TD}><span className="font-mono text-[11px] font-semibold mr-1.5">{p.country}</span>{countryName(p.country)}</td>
                      <td className={`${ST_TD} text-muted`}>{p.city || '—'}</td>
                      <td className={ST_TD}>{p.ok ? <Badge tone="ok">Reachable</Badge> : <Badge tone="danger">Failed</Badge>}</td>
                      <td className={`${ST_TD} text-right font-mono`}>{p.code || '—'}</td>
                      <td className={`${ST_TD} text-right font-mono`}>{p.latencyMs > 0 ? ms(p.latencyMs) : '—'}</td>
                      <td className={`${ST_TD} text-muted max-w-xs truncate`} title={p.detail}>{p.detail || ''}</td>
                    </tr>
                  ))}
                  
                </tbody>
              </table>
            </SettingsTable>
            )}
          </div>
        )}
      </SettingsSection>

      <SettingsSection title="Recent outage confirmations" description="How each recent outage looked from the worldwide countries.">
        {data.recent.length === 0 ? (
          <EmptyState compact title="No confirmations yet">
            {data.enabled ? 'They appear here when a monitor goes down.' : 'Turn on “Also confirm every outage worldwide” above.'}
          </EmptyState>
        ) : (
          <>
          <ListFrame>
            {recentPaging.rows.map(r => (
              <div key={r.outageId} className={`${LIST_ROW} flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]`}>
                <Link href={`/monitors/${r.monitorId}`} className="min-w-0 max-w-[16rem] truncate font-medium text-body hover:text-accent">{r.monitorName || `Monitor ${r.monitorId}`}</Link>
                <span className="text-muted tnum">{dateServer(r.startedAt)}</span>
                <RegionsBadge rc={r.regions} />
                <span className="flex flex-wrap gap-1">
                  {(r.regions?.probes ?? []).map((p, i) => (
                    <span key={i} title={`${countryName(p.country)}${p.city ? ` · ${p.city}` : ''}: ${p.ok ? 'reachable' : p.detail || 'failed'}`}
                      className={`rounded-md border px-1.5 py-px font-mono text-[11px] ${p.ok ? 'border-ok/40 bg-ok/5 text-ok' : 'border-danger/40 bg-danger/5 text-danger'}`}>{p.country}</span>
                  ))}
                </span>
              </div>
            ))}
          </ListFrame>
          <Pager paging={recentPaging} noun="Outages" sizes={RECENT_SIZES} />
          </>
        )}
      </SettingsSection>
    </>
  )
}
