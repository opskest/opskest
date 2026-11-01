import { memo, useEffect, useState } from 'react'
import { duration, serverZoneLabel, timeServer } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import type { Connectivity } from '../../lib/live'
import { PROBLEM_ORDER, STATUS_LABEL } from '../../lib/status'
import { statusColor } from '../Status'
import type { Cluster, Status } from '../../lib/types'
import type { PageAlarm } from '../../hooks/usePageAlarm'
import Brand from '../Brand'
import { Clusters } from '../Clusters'
import { ThemeSliderSwitch } from '../PageShell'

function useSecondTick() {
  const [, setN] = useState(0)
  useEffect(() => {
    const t = window.setInterval(() => setN(n => n + 1), 1000)
    return () => window.clearInterval(t)
  }, [])
}

function StripClock({ receivedAt }: { receivedAt: number }) {
  useSecondTick()
  const now = Date.now()
  const age = receivedAt ? now - receivedAt : 0
  return (
    <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 min-w-0">
      <span className={`font-mono text-[13px] tnum whitespace-nowrap ${age > 30_000 ? 'text-warn font-medium' : 'text-muted'}`}>
        {receivedAt ? `updated ${duration(age)} ago` : 'waiting for data'}
      </span>
      <time className="font-mono text-[20px] font-semibold tnum text-body whitespace-nowrap">
        {new Intl.DateTimeFormat(undefined, { timeZone: serverTimeZone(), hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(now))} <span className="text-[13px] font-medium text-muted">{serverZoneLabel(now)}</span>
      </time>
    </div>
  )
}

export function ReconnectingBanner({ receivedAt }: { receivedAt: number }) {
  useSecondTick()
  return (
    <div role="status" className="shrink-0 px-4 py-2 bg-danger-bg text-danger text-[15px] font-medium flex items-center gap-2">
      <span aria-hidden="true" className="w-3.5 h-3.5 rounded-full border-2 border-current border-t-transparent animate-spin motion-reduce:animate-none" />
      Reconnecting…{receivedAt ? ` last update ${duration(Date.now() - receivedAt)} ago — showing the last known state.` : ''}
    </div>
  )
}

function Totals({ counts }: { counts: Record<Status, number> }) {
  const shown = [...PROBLEM_ORDER, 'up' as const].filter(s => counts[s] > 0)
  return (
    <p className="flex items-center flex-wrap gap-x-3 gap-y-0.5 text-[15px] font-medium tnum min-w-0" aria-label="Totals">
      {shown.map(s => (
        <span key={s} className={`inline-flex items-center gap-1.5 ${statusColor(s).text}`}>
          <span aria-hidden="true" className={`w-2.5 h-2.5 rounded-full ${statusColor(s).dot}`} />
          {counts[s]} {STATUS_LABEL[s].toLowerCase()}
        </span>
      ))}
      {counts.paused > 0 && <span className="text-muted">{counts.paused} paused</span>}
    </p>
  )
}

export const TopStrip = memo(function TopStrip({
  counts, clusters, connectivity, receivedAt, title, exitHref, viewSwitcher, showThemeSlider = true, alarm,
}: {
  counts: Record<Status, number>
  clusters: Cluster[]
  connectivity: Connectivity
  receivedAt: number
  exitHref?: string
  title?: string
  viewSwitcher?: React.ReactNode
  showThemeSlider?: boolean
  alarm?: PageAlarm
}) {
  return (
    <>
      <header className="relative shrink-0 flex flex-wrap items-center gap-x-5 gap-y-3 px-4 sm:px-5 py-3 bg-ground border-b border-default select-none">
        <div className="min-w-0 flex items-center gap-4"><Brand size="md" />{title && <span className="text-[18px] font-semibold text-body truncate">{title}</span>}{exitHref && <a href={exitHref} className="text-[12px] text-muted hover:text-body shrink-0">Status page</a>}</div>

        <Totals counts={counts} />
        <Clusters clusters={clusters} size="lg" />

        <div className="ml-auto flex flex-wrap items-center justify-end gap-3 min-w-0">
          {viewSwitcher && <div className="flex items-center shrink-0">{viewSwitcher}</div>}
          {alarm && (
            <div className="flex flex-wrap items-center gap-2 min-w-0">
              {alarm.blocked && !alarm.playing && (
                <span className="text-xs font-medium text-warn max-w-[32ch]">Sound blocked by the browser. Click anywhere to enable.</span>
              )}
              <button
                type="button"
                onClick={alarm.toggle}
                aria-pressed={alarm.enabled}
                className={`text-xs font-semibold h-8 px-3 rounded-lg border transition ${
                  alarm.playing
                    ? 'bg-danger-bg text-danger border-danger ring-2 ring-danger/40 animate-pulse motion-reduce:animate-none'
                    : alarm.enabled
                    ? 'bg-surface-2 text-body border-default hover:border-strong'
                    : 'bg-surface-2/40 text-muted border-default'
                }`}
                title={alarm.playing ? 'Silence this alarm' : alarm.enabled
                  ? 'Sounds 10s when a monitor goes down, 5s when one degrades. Click to mute.'
                  : 'Alarm muted. Click to turn it on.'}
              >
                {alarm.playing ? 'Silence alarm' : alarm.enabled ? 'Alarm on' : 'Alarm muted'}
              </button>
            </div>
          )}
          {showThemeSlider && <div className="flex items-center shrink-0"><ThemeSliderSwitch /></div>}
          <StripClock receivedAt={receivedAt} />
        </div>
      </header>
      {!connectivity.online && (
        <div role="alert" className="shrink-0 px-4 py-2 bg-warn-bg text-warn text-[15px] font-medium">
          Opskest lost internet connectivity{connectivity.since ? ` at ${timeServer(connectivity.since)}` : ''} — failing checks are held as
          unknown until it is back.
        </div>
      )}
    </>
  )
})
