import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { buildCards, type BoardGroup, type BoardMonitor } from '../../lib/boardLayout'
import { FULL_MIN_EM, LEAN_MIN_EM, isProblem, nameWraps, pageTilesOf, planBoard, planGroups, type FitItem } from '../../lib/boardFit'
import { GroupCard } from './GroupCard'
import { MonitorCard } from './MonitorCard'
import { Icon } from '../icons'

export interface BoardProps {
  groups: BoardGroup[]
  monitors: ReadonlyMap<number, BoardMonitor>
  mode: 'private' | 'public'
  showReason?: boolean
  showLatency?: boolean
  cols?: number
  sort?: 'fixed' | 'severity'
  now?: number
  linkMonitors?: boolean
  emptyText?: string
}

const COARSE_TICK_MS = 15_000
const ROTATE_MS = 10_000
const PAD = 12
const VIEW_KEY = 'opskest-board-view'
const GROUP_PAGE = 16

function readView(): 'services' | 'groups' {
  try { return localStorage.getItem(VIEW_KEY) === 'groups' ? 'groups' : 'services' } catch { return 'services' }
}
function remember(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* private mode */ } }

function useCoarseNow(enabled: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const t = window.setInterval(() => setNow(Date.now()), COARSE_TICK_MS)
    return () => window.clearInterval(t)
  }, [enabled])
  return now
}

function useBoxSize<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [size, setSize] = useState({ w: 0, h: 0 })

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => {
      const w = Math.floor(el.clientWidth)
      const h = Math.floor(el.clientHeight)
      setSize(s => (s.w === w && s.h === h ? s : { w, h }))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  return [ref, size] as const
}

function Segment<T extends string | number>({ label, options, value, onChange }: { label: string; options: { key: T; label: string }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div role="group" aria-label={label} className="inline-flex items-center rounded-[10px] bg-surface-2 p-0.5 text-[12px] font-medium">
      {options.map(o => (
        <button key={String(o.key)} type="button" aria-pressed={value === o.key} onClick={() => onChange(o.key)}
          className={`h-7 px-3 rounded-lg transition-colors tnum ${value === o.key ? 'bg-surface text-body font-semibold' : 'text-muted hover:text-body'}`}>{o.label}</button>
      ))}
    </div>
  )
}

export function Board({
  groups, monitors, mode, showReason = true, showLatency = true, sort = 'fixed',
  now: nowProp, linkMonitors = false, emptyText = 'No monitors to show.',
}: BoardProps) {
  const localNow = useCoarseNow(nowProp == null)
  const now = nowProp ?? localNow
  const [ref, size] = useBoxSize<HTMLDivElement>()
  const kiosk = typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('kiosk') === '1'

  // Services (one tile per monitor) or Groups; Fit all shows everything, 8 / 12 / 16 show that many per screen and rotate.
  const [viewMode, setViewModeState] = useState<'services' | 'groups'>(readView)
  const [pageIndex, setPageIndex] = useState(0)
  const [isRotating, setIsRotating] = useState(true)
  const barRef = useRef<HTMLDivElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  // The browser may lay a tile out taller than the estimate: lower the height
  // budget until nothing overflows (the board never scrolls).
  const [budget, setBudget] = useState(1)
  const setViewMode = (v: 'services' | 'groups') => { setViewModeState(v); setPageIndex(0); remember(VIEW_KEY, v) }

  const groupCards = useMemo(() => buildCards(groups, monitors, now, sort), [groups, monitors, now, sort])

  const monitorList = useMemo(() => Array.from(monitors.values()), [monitors])
  const priv = mode === 'private'
  const wantReason = priv && showReason

  const fitItems = useMemo<FitItem[]>(() => monitorList.map(m => ({
    key: m.id, name: m.name, status: m.paused ? 'paused' : m.status, reason: wantReason ? m.reason : undefined, type: m.type, beats: !!m.beats,
  })), [monitorList, wantReason])
  const byId = useMemo(() => new Map(monitorList.map(m => [m.id, m])), [monitorList])

  const areaW = Math.max(0, size.w - PAD * 2)
  const areaH = Math.max(0, size.h - PAD * 2)
  const plan = useMemo(
    () => planBoard(fitItems, { w: areaW, h: areaH, showReason: wantReason, budget }),
    [fitItems, areaW, areaH, wantReason, budget],
  )
  // A single row (or a lone card) is centred at a natural card width instead of stretching across the whole screen.
  const naturalCols = Math.max(1, Math.floor((areaW + plan.gap) / ((plan.tier === 'full' ? FULL_MIN_EM : LEAN_MIN_EM) * plan.fs + plan.gap)))
  const widthFrac = Math.min(1, plan.cols / naturalCols)
  const planSig = `${areaW}x${areaH}|${fitItems.length}`
  useEffect(() => { setBudget(1) }, [planSig])

  const totalItems = viewMode === 'services' ? monitorList.length : groupCards.length
  const totalPages = viewMode === 'services' ? plan.pages : Math.max(1, Math.ceil(groupCards.length / GROUP_PAGE))

  useEffect(() => {
    if (pageIndex >= totalPages) setPageIndex(0)
  }, [totalPages, pageIndex])

  // Rotation (only when there is more than one page): one timeout per page,
  // restarted when the page changes, with a CSS transition for the bar.
  useEffect(() => {
    const bar = barRef.current
    if (bar) {
      bar.style.transition = 'none'
      bar.style.width = '0%'
    }
    if (!isRotating || totalPages <= 1) return
    if (bar) {
      void bar.offsetWidth
      bar.style.transition = `width ${ROTATE_MS}ms linear`
      bar.style.width = '100%'
    }
    const timer = window.setTimeout(() => setPageIndex(prev => (prev + 1) % totalPages), ROTATE_MS)
    return () => window.clearTimeout(timer)
  }, [isRotating, totalPages, pageIndex])

  const pageTiles = useMemo(() => pageTilesOf(plan, pageIndex), [plan, pageIndex])

  // Overflow guard for the auto views. The grid sits inside a centring wrapper, so compare its own height with the room it has.
  // It re-checks whenever the grid changes size (web fonts arriving, names wrapping) and once fonts are ready, not only on render,
  // so a page that came out one row too tall on a fresh load is corrected at once instead of at the next rotation.
  useEffect(() => {
    const g = gridRef.current
    if (!g || areaH <= 0) return
    const check = () => {
      if (g.offsetHeight > areaH + 1) setBudget(b => (b > 0.5 ? Math.max(0.5, +(b - 0.05).toFixed(2)) : b))
    }
    check()
    const ro = new ResizeObserver(check)
    ro.observe(g)
    let live = true
    void document.fonts?.ready.then(() => { if (live) check() })
    return () => { live = false; ro.disconnect() }
  }, [areaH, budget, viewMode, pageIndex, plan.cols, plan.fs, plan.pages])

  const pagedGroups = useMemo(() => {
    const start = pageIndex * GROUP_PAGE
    return groupCards.slice(start, start + GROUP_PAGE)
  }, [groupCards, pageIndex])

  // Groups: natural-height cards at the largest type size that fits (Fit all), or exactly N cells per screen.
  const groupFit = useMemo(() => planGroups(pagedGroups.map(c => c.problems.length), areaW, areaH * budget), [pagedGroups, areaW, areaH, budget])
  const problemCount = useMemo(() => fitItems.filter(i => isProblem(i.status)).length, [fitItems])
  // Does any tile on the board wrap its name? Then every tile on every page reserves two lines, so all pages have the same tile height.
  const reserveName = useMemo(() => {
    const tw = (areaW - plan.gap * (plan.cols - 1)) / Math.max(1, plan.cols)
    const cfg = { fs: plan.fs, tier: plan.tier, minEm: plan.tier === 'full' ? FULL_MIN_EM : LEAN_MIN_EM }
    return plan.tiles.some(t => nameWraps(t.name, tw, cfg))
  }, [plan.tiles, plan.tier, areaW, plan.gap, plan.cols, plan.fs])
  const groupRows = useMemo(() => Math.max(0, ...pagedGroups.map(c => Math.min(c.problems.length, groupFit.maxLines) + (c.problems.length > groupFit.maxLines ? 1 : 0))), [pagedGroups, groupFit.maxLines])
  const reserveGroupName = useMemo(() => {
    const tw = (areaW - groupFit.gap * (groupFit.cols - 1)) / Math.max(1, groupFit.cols)
    const inner = tw / groupFit.fs - 2
    return groupCards.some(c => c.name.length * 0.64 > Math.max(4, inner - 8))
  }, [groupCards, areaW, groupFit.gap, groupFit.cols, groupFit.fs])
  const tile = (m: BoardMonitor, tier: 'full' | 'lean') => (
    <MonitorCard key={m.id} monitor={m} mode={mode} showReason={wantReason} showLatency={showLatency} linkMonitors={priv && linkMonitors} now={now} tier={tier} reserveName={reserveName} />
  )

  return (
    <div className={`relative h-full w-full flex flex-col overflow-hidden bg-ground text-body ${kiosk ? 'cursor-none' : ''}`}>
      {/* Slim toolbar in the layout (never over the tiles): what to show, how many, rotation. ?kiosk=1 removes it. */}
      {!kiosk && <div className="shrink-0 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-2 bg-ground border-b border-default text-[13px] select-none">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <Segment label="Show" value={viewMode} onChange={setViewMode} options={[{ key: 'services', label: `Services ${monitorList.length}` }, { key: 'groups', label: `Groups ${groupCards.length}` }]} />
          {viewMode === 'services' && plan.pages > 1 && (
            <span className="text-[12px] text-muted tnum">{problemCount} need attention, shown on every page · {monitorList.length - problemCount} others rotate</span>
          )}
        </div>

        {totalPages > 1 && (
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1.5">
              <button type="button" onClick={() => setIsRotating(r => !r)} className="p-1 rounded hover:bg-surface-2 text-muted hover:text-body transition" title={isRotating ? 'Pause auto-rotation' : 'Resume rotation'} aria-label={isRotating ? 'Pause auto-rotation' : 'Resume rotation'}>
                {isRotating ? <Icon.Pause className="w-3.5 h-3.5 text-accent" /> : <Icon.Play className="w-3.5 h-3.5 text-muted" />}
              </button>
              <div className="w-16 h-1.5 bg-surface-2 rounded-full overflow-hidden" title={`${ROTATE_MS / 1000}s rotation timer`}>
                <div ref={barRef} className="h-full bg-accent rounded-full" style={{ width: '0%' }} />
              </div>
            </div>
            <span className="text-[12px] text-muted tnum font-medium">Page {pageIndex + 1} of {totalPages}</span>
            <div className="flex items-center gap-0.5">
              <button type="button" onClick={() => setPageIndex(p => (p - 1 + totalPages) % totalPages)} className="p-1 rounded hover:bg-surface-2 text-muted hover:text-body" title="Previous page" aria-label="Previous page"><Icon.ArrowLeft className="w-4 h-4" /></button>
              <button type="button" onClick={() => setPageIndex(p => (p + 1) % totalPages)} className="p-1 rounded hover:bg-surface-2 text-muted hover:text-body" title="Next page" aria-label="Next page"><Icon.ArrowRight className="w-4 h-4" /></button>
            </div>
          </div>
        )}
      </div>}

      {/* The board: always exactly the available size, never scrolls. */}
      <div ref={ref} className="flex-1 min-h-0 w-full overflow-hidden" style={{ padding: PAD }}>
        {totalItems === 0 ? (
          <div className="h-full flex items-center justify-center text-muted text-base px-4 text-center">{emptyText}</div>
        ) : viewMode === 'services' ? (
          <div className="h-full flex flex-col justify-center">
            <div
              ref={gridRef}
              className="grid mx-auto"
              style={{ gridTemplateColumns: `repeat(${plan.cols}, minmax(0, 1fr))`, gap: plan.gap, fontSize: plan.fs, width: `${(widthFrac * 100).toFixed(2)}%` }}
            >
              {pageTiles.map(t => { const m = byId.get(t.key as number); return m ? tile(m, plan.tier) : null })}
            </div>
          </div>
        ) : (
          <div className="h-full flex flex-col justify-center">
            <div
              ref={gridRef}
              className="grid"
              style={{ gridTemplateColumns: `repeat(${groupFit.cols}, minmax(0, 1fr))`, gap: groupFit.gap, fontSize: groupFit.fs }}
            >
              {pagedGroups.map(card => (
                <GroupCard key={card.key} card={card} mode={mode} linkMonitors={priv && linkMonitors} now={now} maxLines={groupFit.maxLines} rows={groupRows} reserveName={reserveGroupName} />
              ))}
            </div>
          </div>
        )}
      </div>

      {totalPages > 1 && (
        <div className="absolute bottom-1 inset-x-0 flex items-center justify-center gap-1.5 pointer-events-none" aria-hidden="true">
          {Array.from({ length: totalPages }).map((_, idx) => (
            <span key={idx} className={`h-1.5 rounded-full transition-all ${pageIndex === idx ? 'w-6 bg-accent' : 'w-1.5 bg-default'}`} />
          ))}
        </div>
      )}
    </div>
  )
}
