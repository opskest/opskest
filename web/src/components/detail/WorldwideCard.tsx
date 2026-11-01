import { useCallback, useEffect, useState } from 'react'
import { api, errorText } from '../../lib/api'
import { countryName } from '../../lib/countries'
import { dateServer, duration, ms } from '../../lib/format'
import { serverRegion } from '../../lib/runtime'
import { Button, Icon, useToast } from '../ui'

// A monitor's worldwide picture: the latest result from every country it
// is checked from (rotating Globalping checks), which countries are
// failing, and when it's next checked. This server checks on every interval.

interface Result { country: string; city: string; ok: boolean; code: number; latencyMs: number; detail: string; checkedAt: number }
interface Status { eligible: boolean; reason?: string; countries: string[]; results: Result[]; lastAt: number; nextInS: number }

export function WorldwideCard({ monitorId, isAdmin }: { monitorId: number; isAdmin: boolean }) {
  const toast = useToast()
  const [st, setSt] = useState<Status | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    api.get<Status>(`/api/monitors/${monitorId}/regions`).then(s => setSt(s && { ...s, countries: s.countries ?? [], results: s.results ?? [] })).catch(() => setSt(null))
  }, [monitorId])
  useEffect(() => {
    setSt(null)
    load()
    const t = window.setInterval(load, 60_000)
    return () => window.clearInterval(t)
  }, [load])

  async function checkNow() {
    setBusy(true)
    try {
      const s = await api.post<Status>(`/api/monitors/${monitorId}/regions/check`)
      setSt({ ...s, countries: s.countries ?? [], results: s.results ?? [] })
    } catch (e) {
      toast.push(errorText(e, 'Worldwide check failed'), 'danger')
    } finally {
      setBusy(false)
    }
  }

  if (!st) return null
  const failing = st.results.filter(r => !r.ok)
  const byCountry = new Map(st.results.map(r => [r.country, r]))
  const pending = st.countries.filter(c => !byCountry.has(c))

  return (
    <div className="rounded-[14px] bg-surface border border-default overflow-hidden">
      <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-4 pb-3">
        <div className="min-w-0">
          <h3 className="text-base font-semibold text-body flex items-center gap-2"><Icon.Globe className="w-4 h-4" />Check locations</h3>
          <p className="text-sm text-muted mt-2">
            {st.eligible
              ? <>{serverRegion()} every check · {st.countries.length} other countries on rotation
                {st.lastAt ? <> · last {duration(Date.now() - st.lastAt)} ago</> : ' · not checked yet'}
                {st.nextInS > 0 && <> · next in ~{duration(st.nextInS * 1000)}</>}</>
              : <>Checked from {serverRegion().toLowerCase() === 'this server' ? 'this server' : serverRegion()} only — {st.reason}</>}
          </p>
        </div>
        {st.eligible && (
          <div className="flex items-center gap-2">
            {st.results.length > 0 && (
              <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full tnum ${failing.length ? 'bg-danger/10 text-danger' : 'bg-ok/10 text-ok'}`}>
                {failing.length ? `Failing from ${failing.length} of ${st.results.length}` : `OK from all ${st.results.length}`}
              </span>
            )}
            {isAdmin && <Button size="sm" busy={busy} onClick={checkNow} icon={<Icon.Refresh className="w-3.5 h-3.5" />}>Check worldwide now</Button>}
          </div>
        )}
      </div>

      {st.eligible && failing.length > 0 && (
        <div className="mx-5 mb-3 rounded-lg border border-l border-danger/30 border-l-danger bg-danger/[0.04] px-4 py-3">
          <div className="flex items-center gap-2 text-[13px] font-semibold text-body">
            <Icon.AlertTriangle className="w-4 h-4 text-danger" />
            Not reachable from {failing.map(f => countryName(f.country)).join(', ')}
          </div>
          <ul className="mt-1.5 space-y-0.5 text-[12px] text-body-2">
            {failing.map(f => (
              <li key={f.country}><span className="font-medium">{countryName(f.country)}{f.city ? ` (${f.city})` : ''}</span> — {f.detail || 'failed'}</li>
            ))}
          </ul>
          <p className="text-[12px] text-muted mt-1.5">Other countries reach it, so this is regional: routing or an ISP in those countries, a CDN / edge location there, or geo-blocking.</p>
        </div>
      )}

      {st.eligible && (
        <div className="divide-y divide-default px-5 pb-5">
          {[...st.results].sort((a, b) => Number(a.ok) - Number(b.ok) || countryName(a.country).localeCompare(countryName(b.country))).map(r => (
            <div key={r.country} title={`${countryName(r.country)}${r.city ? ` · ${r.city}` : ''} · ${dateServer(r.checkedAt)}${r.detail ? `\n${r.detail}` : ''}`}
              className={`py-4 min-w-0 ${r.ok ? '' : 'text-danger'}`}>
              <div className="flex items-center justify-between gap-2">
                <span className="flex items-center gap-1.5 min-w-0">
                  <span className={`w-2 h-2 rounded-full shrink-0 ${r.ok ? 'bg-ok' : 'bg-danger'}`} />
                  <span className="font-mono text-xs text-muted">{r.country}</span>
                  <span className="text-sm text-body truncate">{countryName(r.country)}</span>
                </span>
                <span className={`text-[12px] font-mono tnum shrink-0 ${r.ok ? 'text-body-2' : 'text-danger'}`}>{r.ok ? (r.latencyMs > 0 ? ms(r.latencyMs) : 'OK') : (r.code ? r.code : 'fail')}</span>
              </div>
              <div className="text-[11px] text-faint truncate mt-0.5">{r.ok ? (r.city || ' ') : (r.detail || 'failed')}</div>
            </div>
          ))}
          {pending.map(c => (
            <div key={c} className="py-4 text-sm text-muted">
              <span className="font-mono text-[11px] font-semibold mr-1.5">{c}</span>{countryName(c)} — waiting for its turn
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
