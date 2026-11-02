// Wallboard helpers shared by /wallboard and /status/:slug/board.

import { useEffect, useMemo, useState } from 'react'
import { applyTheme, getTheme } from '../lib/theme'

const COARSE_TICK_MS = 15_000

/** `?theme=` applies while the board is open; leaving restores the saved choice. */
export function useThemeOverride(theme: 'light' | 'dark' | null) {
  useEffect(() => {
    if (!theme) return
    applyTheme(theme)
    return () => {
      applyTheme(getTheme())
    }
  }, [theme])
}

/** "Now" for durations and the stale rule: moves on every delta and every
 * 15 s between them, so stale markers appear even if the stream goes quiet. */
export function useCoarseNow(lastUpdate: number): number {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const t = window.setInterval(() => setTick(n => n + 1), COARSE_TICK_MS)
    return () => window.clearInterval(t)
  }, [])
  return useMemo(() => {
    void lastUpdate
    void tick
    return Date.now()
  }, [lastUpdate, tick])
}
