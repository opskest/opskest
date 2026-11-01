// Fit maths for the NOC wallboard (Services view): a TV or monitor shows
// every problem at once, without scrolling and without stretching cards to
// fill the screen. Cards keep their natural, multi-line height; the plan
// picks the largest readable type size at which all of them fit; when they do
// not, the board becomes a slideshow: every problem stays on every page and
// the other services rotate through the remaining space.
// Pure maths, no DOM.

import type { Status } from './types'

export interface FitItem {
  key: string | number
  name: string
  status: Status
  reason?: string
  type?: string
  beats?: boolean
}

export type Tier = 'full' | 'lean'

export interface FitConfig { fs: number; tier: Tier; minEm: number }

export interface FitOptions {
  w: number
  h: number
  showReason: boolean
  /** Fraction of the height the plan may use (the view lowers it if the browser lays out taller than estimated). */
  budget?: number
  /** Every item of the board: if any wraps its name, all tiles reserve two lines, so every page has the same tile height. */
  all?: FitItem[]
}

export interface FitPlan {
  fs: number
  tier: Tier
  cols: number
  gap: number
  /** Items of this page, worst first. */
  tiles: FitItem[]
  /** Always 0 now (healthy services are shown in a slideshow instead of being folded away). */
  folded: number
  pages: number
  /** Problems kept on every page of a slideshow. */
  pinned: number
  /** Other services per page of a slideshow. */
  perPage: number
}

export const FULL_MIN_EM = 21
export const LEAN_MIN_EM = 12.5

/** Largest first. Full cards at generous sizes, then lean ones. */
export const CONFIGS: FitConfig[] = [
  ...[64, 56, 48, 42, 38, 34, 30, 26, 22, 20, 18, 16, 14].map(fs => ({ fs, tier: 'full' as Tier, minEm: FULL_MIN_EM })),
  ...[22, 20, 18, 16, 14, 13, 12, 11].map(fs => ({ fs, tier: 'lean' as Tier, minEm: LEAN_MIN_EM })),
]

export const isProblem = (s: Status): boolean => s === 'down' || s === 'degraded' || s === 'pending' || s === 'unknown'

/** Problems first (worst first), then everything else by name. */
export function orderItems<T extends FitItem>(items: T[]): T[] {
  const rank: Record<string, number> = { down: 0, pending: 1, degraded: 2, unknown: 3, maintenance: 4, up: 5, paused: 6 }
  return [...items].sort((a, b) => (rank[a.status] ?? 9) - (rank[b.status] ?? 9) || a.name.localeCompare(b.name))
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

/** Natural height of one card in em (matches MonitorCard's markup). */
/** Would this name wrap onto a second line in a tile of width tw (px) at cfg? */
export function nameWraps(name: string, tw: number, cfg: FitConfig): boolean {
  const inner = tw / cfg.fs - 2
  return name.length * 0.64 > Math.max(4, inner - 6)
}

export function cardEm(item: FitItem, tw: number, cfg: FitConfig, showReason: boolean, reserveTwo = false): number {
  const inner = tw / cfg.fs - 2
  const nameLines = reserveTwo ? 2 : clamp(Math.ceil((item.name.length * 0.64) / Math.max(4, inner - 6)), 1, 2)
  const header = Math.max(1.3, nameLines * 1.32)
  const reasonLines = showReason && item.status === 'down' && item.reason ? Math.min(3, Math.ceil((item.reason.length * 0.5) / Math.max(4, inner - 0.5))) : 0
  const strip = cfg.tier === 'full' ? 1.7 + 0.7 : 0
  const reason = reasonLines ? 0.7 + reasonLines * 0.95 : 0
  const footer = 0.7 + 1.15
  return 1.8 + header + strip + reason + footer
}

/** Group card geometry: how big the type can be so every group card fits without scrolling. */
export interface GroupFit { fs: number; cols: number; gap: number; maxLines: number }

export function groupCardEm(problemLines: number): number {
  // padding, name + big fraction, pill strip, then the services that are not up (or one "all operational" line)
  return 1.8 + 2.65 + 0.7 + 1.7 + 0.7 + 1.2 + (problemLines > 0 ? 0.7 + problemLines * 1.55 : 0)
}

export function planGroups(problemCounts: number[], w: number, h: number): GroupFit {
  const n = problemCounts.length
  const sizes = [64, 56, 48, 42, 38, 34, 30, 26, 22, 20, 18, 16, 14, 12, 11]
  const maxFs = Math.max(22, w / 95)
  const tryFit = (fs: number, cap: number) => {
    const gap = Math.round(fs * 0.7)
    const cols = clamp(Math.floor((w + gap) / (14 * fs + gap)), 1, Math.max(1, n))
    let total = 0
    let rows = 0
    for (let i = 0; i < n; i += cols) {
      total += Math.max(...problemCounts.slice(i, i + cols).map(c => groupCardEm(Math.min(c, cap) + (c > cap ? 1 : 0)))) * fs
      rows++
    }
    total += gap * Math.max(0, rows - 1)
    return { ok: total <= h, cols, gap }
  }
  for (const cap of [8, 6, 4, 3, 2]) {
    for (const fs of sizes) {
      if (fs > maxFs) continue
      const r = tryFit(fs, cap)
      if (r.ok) return { fs, cols: r.cols, gap: r.gap, maxLines: cap }
    }
  }
  const fs = 11
  const gap = 8
  return { fs, cols: clamp(Math.floor((w + gap) / (14 * fs + gap)), 1, Math.max(1, n)), gap, maxLines: 2 }
}

/** Rows and columns for exactly n equal cells filling w x h (fixed 8/12/16 layouts): the grid whose cells come nearest a 1.6:1 shape. */
export function gridFor(n: number, w: number, h: number, gap = 14): { cols: number; rows: number; cellW: number; cellH: number } {
  let best = { cols: 1, rows: Math.max(1, n), cellW: w, cellH: h / Math.max(1, n), score: -1 }
  for (let cols = 1; cols <= Math.max(1, n); cols++) {
    const rows = Math.ceil(n / cols)
    const cellW = (w - gap * (cols - 1)) / cols
    const cellH = (h - gap * (rows - 1)) / rows
    if (cellW <= 0 || cellH <= 0) continue
    const empty = (cols * rows - n) / (cols * rows)
    const score = Math.min(cellW / 1.6, cellH) * (1 - empty * 0.6)
    if (score > best.score) best = { cols, rows, cellW, cellH, score }
  }
  return { cols: best.cols, rows: best.rows, cellW: best.cellW, cellH: best.cellH }
}

/** Type size (px) for a fixed cell: the card is about 10 lines tall and 15 characters-widths wide at 1em. */
export function fsForCell(cellW: number, cellH: number): number {
  return clamp(Math.min(cellH / 9.2, cellW / 18), 10, 48)
}

export function columnsFor(w: number, cfg: FitConfig, count: number): { cols: number; gap: number } {
  const gap = Math.round(cfg.fs * 0.6)
  const cols = clamp(Math.floor((w + gap) / (cfg.minEm * cfg.fs + gap)), 1, Math.max(1, count))
  return { cols, gap }
}

/** Total height in px of these items laid out at cfg. */
export function layoutHeight(items: FitItem[], opts: FitOptions, cfg: FitConfig): { height: number; cols: number; gap: number } {
  const { cols, gap } = columnsFor(opts.w, cfg, items.length)
  const tw = (opts.w - gap * (cols - 1)) / cols
  const reserve = (opts.all ?? items).some(it => nameWraps(it.name, tw, cfg))
  let total = 0
  let rows = 0
  for (let i = 0; i < items.length; i += cols) {
    let rowEm = 0
    for (const it of items.slice(i, i + cols)) rowEm = Math.max(rowEm, cardEm(it, tw, cfg, opts.showReason, reserve))
    total += rowEm * cfg.fs
    rows++
  }
  return { height: total + gap * Math.max(0, rows - 1), cols, gap }
}

function bestConfig(items: FitItem[], opts: FitOptions): { cfg: FitConfig; cols: number; gap: number } | null {
  const avail = opts.h * (opts.budget ?? 1)
  // Type scales with the screen: a 4K wall display gets proportionally larger text, a laptop never goes above 34.
  const maxFs = Math.max(22, opts.w / 95)
  for (const cfg of CONFIGS) {
    if (cfg.tier === 'full' && cfg.fs > maxFs) continue
    const r = layoutHeight(items, opts, cfg)
    if (r.height <= avail) return { cfg, cols: r.cols, gap: r.gap }
  }
  return null
}

const MIN_READABLE_FS = 14
const MIN_PAGE_TILES = 20

export function planBoard(all: FitItem[], baseOpts: FitOptions): FitPlan {
  const items = orderItems(all)
  const opts: FitOptions = { ...baseOpts, all: items }
  const n = items.length
  const empty: FitPlan = { fs: CONFIGS[0].fs, tier: 'full', cols: 1, gap: 12, tiles: [], folded: 0, pages: 1, pinned: 0, perPage: 0 }
  if (n === 0 || opts.w <= 0 || opts.h <= 0) return empty

  // Everything on one screen, as long as the text stays readable.
  const whole = bestConfig(items, opts)
  if (whole && whole.cfg.fs >= MIN_READABLE_FS) {
    return { fs: whole.cfg.fs, tier: whole.cfg.tier, cols: whole.cols, gap: whole.gap, tiles: items, folded: 0, pages: 1, pinned: 0, perPage: n }
  }

  // Too many for one readable screen: a slideshow. Every problem stays on every page; the rest rotate.
  const avail = opts.h * (opts.budget ?? 1)
  const maxFs = Math.max(22, opts.w / 95)
  const problems = items.filter(i => isProblem(i.status)).length
  const capacity = (cfg: FitConfig) => {
    let lo = 1, hi = n
    while (lo < hi) {
      const mid = Math.ceil((lo + hi) / 2)
      if (layoutHeight(items.slice(0, mid), opts, cfg).height <= avail) lo = mid; else hi = mid - 1
    }
    return lo
  }
  const fulls = CONFIGS.filter(c => c.tier === 'full' && c.fs <= maxFs && c.fs >= MIN_READABLE_FS)
  let chosen = fulls[fulls.length - 1] ?? CONFIGS[CONFIGS.length - 1]
  let k = capacity(chosen)
  for (const cfg of fulls) {
    const c = capacity(cfg)
    if (c >= Math.min(n, MIN_PAGE_TILES)) { chosen = cfg; k = c; break }
  }
  const { cols, gap } = layoutHeight(items.slice(0, k), opts, chosen)
  const pinned = k > problems ? problems : 0
  const perPage = Math.max(1, k - pinned)
  const pages = Math.max(1, Math.ceil((n - pinned) / perPage))
  return { fs: chosen.fs, tier: chosen.tier, cols, gap, tiles: items, folded: 0, pages, pinned, perPage }
}

/** The tiles of one page: the pinned problems, then that page's slice of the rest. */
export function pageTilesOf(plan: FitPlan, index: number): FitItem[] {
  if (plan.pages <= 1) return plan.tiles
  const head = plan.tiles.slice(0, plan.pinned)
  const rest = plan.tiles.slice(plan.pinned)
  return [...head, ...rest.slice(index * plan.perPage, (index + 1) * plan.perPage)]
}

/** Slice of `tiles` for one page (equal split), for lists without pinned items. */
export function pageOf<T>(tiles: T[], pages: number, index: number): T[] {
  if (pages <= 1) return tiles
  const size = Math.ceil(tiles.length / pages)
  return tiles.slice(index * size, index * size + size)
}
