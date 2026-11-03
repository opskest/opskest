import { useMemo, useState } from 'react'
import type { Heartbeat } from '../../lib/types'
import { dateServer, ms } from '../../lib/format'
import { Icon } from '../ui'

// Where a check's time went, hop by hop — resolver, network, the edge
// (ALB / Nginx / CDN), the app, the download — judged against this
// monitor's own history (last 50 checks): a waterfall where each layer starts
// where the previous one ended (dashed outline = its usual time), ranked
// findings with what to check, and a strip of recent checks (select one to
// inspect it).

type Key = 'dns' | 'connect' | 'tls' | 'edge' | 'app' | 'server' | 'download' | 'other'

interface Phase { key: Key; ms: number }

const META: Record<Key, { label: string; layer: string; where: string; color: string }> = {
  dns: { label: 'DNS lookup', layer: 'Resolver', where: 'DNS resolver / DNS provider', color: 'bg-purple-500' },
  connect: { label: 'Connect', layer: 'Network', where: 'Network round trip to the edge (ISP, routing, region)', color: 'bg-sky-500' },
  tls: { label: 'TLS handshake', layer: 'Edge', where: 'TLS termination at the ALB / Nginx / CDN', color: 'bg-amber-500' },
  edge: { label: 'Proxy / LB', layer: 'Edge', where: 'Load balancer or proxy: queueing, forwarding to the app', color: 'bg-slate-400' },
  app: { label: 'App processing', layer: 'App', where: 'Time the app reports spending on the request', color: 'bg-emerald-500' },
  server: { label: 'Server response', layer: 'Edge + app', where: 'Request sent → first byte: load balancer + app together', color: 'bg-emerald-500' },
  download: { label: 'Download', layer: 'Transfer', where: 'Receiving the response body', color: 'bg-teal-400' },
  other: { label: 'Redirects & other', layer: 'Earlier hops', where: 'Time outside the final request: redirects followed first, plus anything not timed', color: 'bg-indigo-400' },
}

function phasesOf(b: Heartbeat): Phase[] | null {
  if (b.dnsMs < 0 && b.tcpMs < 0 && b.ttfbMs < 0) return null
  const dns = Math.max(0, b.dnsMs)
  const connect = Math.max(0, b.tcpMs)
  const tls = Math.max(0, b.tlsMs)
  const wait = b.waitMs >= 0 ? b.waitMs : Math.max(0, b.ttfbMs - dns - connect - tls)
  const download = b.ttfbMs >= 0 && b.latencyMs >= 0 ? Math.max(0, b.latencyMs - b.ttfbMs) : 0
  const out: Phase[] = [{ key: 'dns', ms: dns }, { key: 'connect', ms: connect }]
  if (b.tlsMs >= 0) out.push({ key: 'tls', ms: tls })
  if (b.appMs >= 0 && b.appMs <= wait) out.push({ key: 'edge', ms: wait - b.appMs }, { key: 'app', ms: b.appMs })
  else out.push({ key: 'server', ms: wait })
  out.push({ key: 'download', ms: download })
  // The timed layers describe the final request only; a redirect chain or
  // untimed work shows up as the gap to the check's total.
  const gap = b.latencyMs >= 0 ? b.latencyMs - out.reduce((a, p) => a + p.ms, 0) : 0
  if (gap >= 10 && gap >= b.latencyMs * 0.03) out.unshift({ key: 'other', ms: gap })
  return out
}

function quantile(v: number[], q: number): number {
  if (v.length === 0) return 0
  const s = [...v].sort((a, b) => a - b)
  const i = Math.min(s.length - 1, Math.max(0, Math.round(q * (s.length - 1))))
  return s[i]
}

type Health = 'ok' | 'slow' | 'spike'
const HEALTH_TEXT: Record<Health, string> = { ok: 'text-ok', slow: 'text-warn', spike: 'text-danger' }

interface Stats { p50: number; p95: number; max: number }

function health(now: number, st: Stats | undefined, n: number): Health {
  if (!st || n < 5) return 'ok'
  if (now > 2 * st.p50 && now - st.p50 > 80) return 'spike'
  if (now > st.p95 && now - st.p50 > 40) return 'slow'
  return 'ok'
}

function tlsRoundTrips(conn: string): number {
  if (conn.includes('resumed')) return 1
  if (conn.includes('TLS 1.3')) return 1
  if (conn.includes('TLS 1.2') || conn.includes('TLS 1.1') || conn.includes('TLS 1.0')) return 2
  return 1.5
}

interface Finding { key: Key | 'total'; sev: Health; title: string; detail: string; check: string }

function findings(phases: Phase[], stats: Partial<Record<Key, Stats>>, n: number, beat: Heartbeat): Finding[] {
  const out: Finding[] = []
  const get = (k: Key) => phases.find(p => p.key === k)?.ms ?? 0
  const total = phases.reduce((a, p) => a + p.ms, 0)
  const rtt = get('connect')
  const usual = (k: Key) => (stats[k] && n >= 5 ? ` (usually ${ms(stats[k]!.p50)})` : '')

  for (const p of phases) {
    const h = health(p.ms, stats[p.key], n)
    if (h === 'ok') continue
    const tail = usual(p.key)
    switch (p.key) {
      case 'dns':
        out.push({ key: 'dns', sev: h, title: `DNS lookup ${ms(p.ms)}${tail}`, detail: 'Resolving the hostname is slower than normal. This is the DNS resolver Opskest uses or the domain\'s DNS provider — before any request reaches your servers.', check: 'The resolver on this host (or the one set on the monitor), the DNS provider\'s status, and very low TTLs that force a fresh lookup every time.' })
        break
      case 'connect':
        out.push({ key: 'connect', sev: h, title: `Network round trip ${ms(p.ms)}${tail}`, detail: 'Opening the TCP connection takes longer than usual. That is pure network time between this server and your edge: the ISP, routing, packet loss, or the edge being overloaded accepting connections.', check: 'ISP / routing between here and the region (try Settings → Tools → Worldwide check), and the load balancer\'s connection metrics.' })
        break
      case 'tls': {
        const rt = tlsRoundTrips(beat.conn)
        out.push({ key: 'tls', sev: h, title: `TLS handshake ${ms(p.ms)}${tail}`, detail: rtt > 0 && p.ms > rtt * rt * 1.5 + 20
          ? `The handshake needs about ${rt === 1 ? 'one round trip' : 'two round trips'}${beat.conn ? ` (${beat.conn.split(' · ').filter(x => x.startsWith('TLS') || x === 'resumed').join(', ')})` : ''} ≈ ${ms(rtt * rt)} on this network, but took ${ms(p.ms)}. The edge itself is slow to complete TLS.`
          : 'The handshake is slower than usual, in line with a slower network round trip.',
          check: 'CPU on the ALB / Nginx / CDN terminating TLS, the certificate chain length, OCSP stapling, and whether TLS 1.3 / session resumption is enabled.' })
        break
      }
      case 'edge':
        out.push({ key: 'edge', sev: h, title: `Load balancer / proxy ${ms(p.ms)}${tail}`, detail: 'Time between the request arriving at the edge and the app starting on it grew, while the app\'s own time did not account for it.', check: 'Proxy / LB queueing (surge queue, worker limits), upstream keep-alive to the app, and target health.' })
        break
      case 'app':
        out.push({ key: 'app', sev: h, title: `App processing ${ms(p.ms)}${tail}`, detail: 'The app reports spending longer than usual on this request. The network and edge are not the cause.', check: 'The app\'s logs and traces for this endpoint, its database / downstream calls, and CPU or memory pressure.' })
        break
      case 'server':
        out.push({ key: 'server', sev: h, title: `Server response ${ms(p.ms)}${tail}`, detail: 'After the request reached the server, it took longer than usual to start answering. That is the load balancer and the app together; the network up to the edge is fine.', check: 'The app and its database first; then proxy / LB queueing. Add a Server-Timing header to tell the two apart.' })
        break
      case 'other':
        out.push({ key: 'other', sev: h, title: `Redirects & other ${ms(p.ms)}${tail}`, detail: 'More time than usual went outside the final request: redirects the monitor followed first, or work that is not timed layer by layer.', check: 'Whether the URL redirects (http to https, www, trailing slash). Monitor the final address directly to remove this time.' })
        break
      case 'download':
        out.push({ key: 'download', sev: h, title: `Download ${ms(p.ms)}${tail}`, detail: 'Receiving the response body is slower than usual: a bigger payload or reduced bandwidth.', check: 'Response size for this endpoint, compression, and bandwidth on the path.' })
        break
    }
  }
  const order: Record<Health, number> = { spike: 0, slow: 1, ok: 2 }
  out.sort((a, b) => order[a.sev] - order[b.sev] || get(b.key as Key) - get(a.key as Key))

  if (out.length === 0) {
    const top = [...phases].sort((a, b) => b.ms - a.ms)[0]
    if (total < 300) {
      out.push({ key: 'total', sev: 'ok', title: `Healthy — ${ms(total)} end to end`, detail: `Every layer is within its normal range. The biggest share is ${META[top.key].label.toLowerCase()} (${ms(top.ms)}).`, check: '' })
    } else {
      out.push({ key: top.key, sev: 'ok', title: `Normal for this endpoint — ${ms(total)}, mostly ${META[top.key].label.toLowerCase()}`, detail: `Nothing is slower than usual; ${META[top.key].label.toLowerCase()} is simply where most of the time always goes (${Math.round((top.ms / Math.max(1, total)) * 100)}%). To make it faster, that is the layer to work on: ${META[top.key].where.toLowerCase()}.`, check: '' })
    }
  }
  return out
}

/** `newestTs`: time of the newest check overall, so the header can say when the shown one is older (the newest may have failed before any phase was timed). */
export function LatencyBreakdown({ beat: latest, recent, newestTs }: { beat: Heartbeat; recent: Heartbeat[]; newestTs?: number }) {
  const [picked, setPicked] = useState<number | null>(null) // ts of a selected historical check
  const history = useMemo(() => recent.filter(b => b.status !== 'down' && phasesOf(b)), [recent])
  const beat = (picked != null && history.find(b => b.ts === picked)) || latest
  const phases = phasesOf(beat)

  const stats = useMemo(() => {
    const byKey: Partial<Record<Key, number[]>> = {}
    for (const b of history) for (const p of phasesOf(b)!) (byKey[p.key] ??= []).push(p.ms)
    const out: Partial<Record<Key, Stats>> = {}
    for (const k of Object.keys(byKey) as Key[]) {
      const v = byKey[k]!
      out[k] = { p50: quantile(v, 0.5), p95: quantile(v, 0.95), max: Math.max(...v) }
    }
    return out
  }, [history])

  if (!phases) return null
  const n = history.length
  const total = Math.max(1, phases.reduce((a, p) => a + p.ms, 0))
  const found = findings(phases, stats, n, beat)

  const nowTotal = beat.latencyMs >= 0 ? beat.latencyMs : total
  const usualTotal = n >= 5 ? quantile(history.map(b => b.latencyMs), 0.5) : null
  const delta = usualTotal != null ? nowTotal - usualTotal : 0
  const problems = found.filter(f => f.sev !== 'ok')

  const compared = n >= 5
  const top = [...phases].sort((a, b) => b.ms - a.ms)[0]
  const topShare = Math.round((top.ms / total) * 100)
  const worst: Health = problems.some(f => f.sev === 'spike') ? 'spike' : problems.length > 0 ? 'slow' : 'ok'
  const verdict = { ok: 'Normal', slow: 'Slower than usual', spike: 'Spike' }[worst]
  const verdictStyle = { ok: 'bg-ok/10 text-ok', slow: 'bg-warn/15 text-warn', spike: 'bg-danger/15 text-danger' }[worst]
  let offset = 0
  const rows = phases.map(p => { const start = offset; offset += p.ms; return { p, start } })
  const strip = history.slice(0, 30).reverse().map(b => ({ b, ph: phasesOf(b)! }))
  const stripMax = Math.max(1, ...strip.map(s => s.ph.reduce((a, p) => a + p.ms, 0)))
  const gridLines = 'repeating-linear-gradient(to right, transparent 0, transparent calc(25% - 1px), rgb(128 128 128 / 0.2) calc(25% - 1px), rgb(128 128 128 / 0.2) 25%)'
  const cols = 'grid grid-cols-[7.25rem_minmax(0,1fr)_4.5rem] sm:grid-cols-[11rem_minmax(0,1fr)_5.5rem] items-center gap-x-3'

  return (
    <div className="rounded-[14px] bg-surface border border-default overflow-hidden">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-4">
        <div className="min-w-0">
          <h3 className="text-base font-semibold text-body">Request timing</h3>
          <p className="text-sm text-muted mt-1.5">
            {picked != null ? <>Check at {dateServer(beat.ts)} · <button type="button" className="text-accent hover:underline" onClick={() => setPicked(null)}>back to latest</button></> : newestTs != null && latest.ts !== newestTs ? <>Latest check with timings ({dateServer(latest.ts)}); the newest check recorded none</> : 'Latest check'}
            {compared && <> · compared with the last {n} checks</>}
          </p>
        </div>
        {compared && <span className={`text-[11px] font-semibold uppercase tracking-wide px-2.5 py-1 rounded-full ${verdictStyle}`}>{verdict}</span>}
      </div>

      {/* Total and what dominated it */}
      <div className="flex flex-wrap items-end justify-between gap-x-8 gap-y-3 px-5 pt-4 pb-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="text-4xl font-semibold tnum text-body leading-none">{ms(nowTotal)}</span>
            {usualTotal != null && (
              <span className="text-sm text-muted tnum">
                usually {ms(usualTotal)}
                {Math.abs(delta) >= 50 && <span className={`ml-1.5 font-medium ${delta > 0 ? 'text-danger' : 'text-ok'}`}>{delta > 0 ? '+' : '−'}{ms(Math.abs(delta))}</span>}
              </span>
            )}
          </div>
          <p className="text-sm text-body-2 mt-2">
            Mostly <span className="font-semibold text-body">{META[top.key].label.toLowerCase()}</span>
            <span className="text-muted tnum"> · {topShare}% of the time</span>
          </p>
        </div>
      </div>

      {/* Waterfall: each layer starts where the previous one ended */}
      <div className="mx-5 rounded-xl border border-default bg-surface-2/30 py-2">
        <div className={`${cols} px-4 pb-1`} aria-hidden="true">
          <span className="text-[11px] uppercase tracking-wider text-faint">Layer</span>
          <div className="relative h-4 text-[11px] text-faint tnum">
            {[0, 0.25, 0.5, 0.75, 1].map(f => (
              <span key={f} className={`absolute top-0 whitespace-nowrap ${f === 0 ? '' : f === 1 ? '-translate-x-full' : '-translate-x-1/2'}`} style={{ left: `${f * 100}%` }}>{f === 0 ? '0' : ms(Math.round(total * f))}</span>
            ))}
          </div>
          <span className="text-[11px] uppercase tracking-wider text-faint text-right">Time</span>
        </div>
        <ol>
          {rows.map(({ p, start }) => {
            const h = health(p.ms, stats[p.key], n)
            const layerName = (p.key === 'tls' || p.key === 'edge') && beat.edge ? beat.edge.split(' · ').pop() : META[p.key].layer
            const usual = stats[p.key]?.p50
            const share = Math.round((p.ms / total) * 100)
            return (
              <li key={p.key} className={`${cols} px-4 py-1.5 ${h === 'spike' ? 'bg-danger/[0.05]' : h === 'slow' ? 'bg-warn/[0.05]' : ''}`} title={META[p.key].where}>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 min-w-0">
                    <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${META[p.key].color}`} />
                    <span className="text-[13px] text-body truncate">{META[p.key].label}</span>
                    {h !== 'ok' && <span className={`shrink-0 text-[10px] font-bold uppercase tracking-wide px-1.5 py-px rounded-md ${h === 'spike' ? 'bg-danger/15 text-danger' : 'bg-warn/15 text-warn'}`}>{h === 'spike' ? 'Spike' : 'Slow'}</span>}
                  </div>
                  <div className="text-[11px] text-faint truncate pl-4 tnum">{layerName}{compared && usual != null ? ` · usual ${ms(usual)}` : ''}</div>
                </div>
                <div className="relative h-7 rounded-md overflow-hidden" style={{ backgroundImage: gridLines }}>
                  {compared && usual != null && usual > 0 && (
                    <div aria-hidden="true" className="absolute inset-y-0 rounded-md border border-dashed border-faint/60"
                      style={{ left: `${(start / total) * 100}%`, width: `${Math.max((usual / total) * 100, 0.6)}%` }} />
                  )}
                  <div className={`absolute inset-y-1 rounded ${META[p.key].color}`}
                    style={{ left: `${(start / total) * 100}%`, width: `${Math.max((p.ms / total) * 100, 0.6)}%` }} />
                </div>
                <div className="text-right">
                  <div className={`text-[15px] font-semibold tnum leading-tight ${h === 'ok' ? 'text-body' : HEALTH_TEXT[h]}`}>{ms(p.ms)}</div>
                  <div className="text-[11px] text-faint tnum">{share}%</div>
                </div>
              </li>
            )
          })}
        </ol>
        {compared && <p className="px-4 pt-1 text-[11px] text-faint">The dashed outline marks each layer's usual time.</p>}
      </div>

      {/* Recent checks: click one to inspect it */}
      {strip.length >= 2 && (
        <div className="px-5 pt-4 pb-5">
          <div className="flex items-center justify-between text-[11px] text-faint mb-2">
            <span>Recent checks · select one to inspect</span>
          </div>
          <div className="flex items-end gap-[3px] h-10" role="group" aria-label="Recent checks">
            {strip.map(({ b, ph }) => {
              const t = ph.reduce((a, p) => a + p.ms, 0)
              const sel = b.ts === beat.ts
              return (
                <button key={b.ts} type="button" onClick={() => setPicked(b.ts === latest.ts ? null : b.ts)}
                  aria-pressed={sel} aria-label={`Check at ${dateServer(b.ts)}, ${ms(b.latencyMs >= 0 ? b.latencyMs : t)}`} title={`${dateServer(b.ts)} · ${ms(b.latencyMs >= 0 ? b.latencyMs : t)}`}
                  className={`flex-1 min-w-0 h-full flex items-end rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent ${sel ? '' : 'opacity-60 hover:opacity-100'}`}>
                  <span className={`w-full flex flex-col-reverse rounded-sm overflow-hidden ${sel ? 'ring-2 ring-accent' : ''}`} style={{ height: `${Math.max((t / stripMax) * 100, 6)}%` }}>
                    {ph.filter(p => p.ms > 0).map(p => <span key={p.key} className={META[p.key].color} style={{ height: `${(p.ms / Math.max(1, t)) * 100}%` }} />)}
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      )}
      {strip.length < 2 && <div className="h-5" />}

      {/* Findings: only when a layer is slower than usual */}
      {problems.length > 0 && (
        <div className="px-5 pb-5 space-y-2.5">
          {problems.map((f, i) => (
            <div key={i} className={`rounded-lg border border-l px-4 py-3 ${f.sev === 'spike' ? 'border-danger/30 border-l-danger bg-danger/[0.04]' : 'border-warn/30 border-l-warn bg-warn/[0.04]'}`}>
              <div className="flex flex-wrap items-center gap-2">
                <Icon.AlertTriangle className={`w-4 h-4 shrink-0 ${HEALTH_TEXT[f.sev]}`} />
                <span className="text-[13px] font-semibold text-body">{f.title}</span>
                <span className={`text-[10px] font-bold uppercase tracking-wide px-1.5 py-px rounded-md ${f.sev === 'spike' ? 'bg-danger/15 text-danger' : 'bg-warn/15 text-warn'}`}>{f.sev === 'spike' ? 'Spike' : 'Slower than usual'}</span>
              </div>
              <p className="text-[13px] text-body-2 mt-1.5 leading-relaxed">{f.detail}</p>
              {f.check && (
                <div className="mt-2 rounded-lg bg-surface/70 border border-default/60 px-3 py-2 text-[12px] text-body-2">
                  <span className="text-[11px] font-semibold uppercase tracking-wider text-faint block mb-0.5">What to check</span>
                  {f.check}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
