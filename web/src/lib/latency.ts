import type { LatencyRange } from './types'

const H = 3_600_000

/** Span and point width of each ?range= (mirrors the server's latencyRanges). */
export const LATENCY_RANGES: Record<LatencyRange, { spanMs: number; widthMs: number; label: string }> = {
  '1h': { spanMs: H, widthMs: 60_000, label: '1h' },
  '24h': { spanMs: 24 * H, widthMs: 15 * 60_000, label: '24h' },
  '7d': { spanMs: 7 * 24 * H, widthMs: H, label: '7d' },
  '30d': { spanMs: 30 * 24 * H, widthMs: 6 * H, label: '30d' },
  '90d': { spanMs: 90 * 24 * H, widthMs: 24 * H, label: '90d' },
}
