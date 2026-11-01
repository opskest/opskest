import { useCallback, useEffect, useRef, useState } from 'react'
import {
  audioReady, playDegradedAlarm, playDownAlarm, stopCurrentAlarm, unlockAudio,
  type HeavySoundType, type LightSoundType,
} from '../lib/sounds'
import type { Status } from '../lib/types'

// The board alarm: a 10s sound when any monitor on the board newly goes
// DOWN, a 5s softer one when one newly goes DEGRADED. "Newly" is tracked by
// monitor id, not by count — one recovering while another fails still
// alarms. Monitors already down when the board opens don't alarm (the
// board shows them); only transitions after the first snapshot do.
//
// Browsers block audio until the viewer interacts with the page once, so a
// wallboard left alone would stay silent without saying so. `blocked`
// surfaces that, and the first click/keypress anywhere unlocks it.

export interface AlarmConfig {
  muted: boolean
  down: HeavySoundType | ''
  degraded: LightSoundType | ''
}

export interface PageAlarm {
  enabled: boolean
  playing: boolean
  blocked: boolean
  toggle: () => void
}

const DEFAULT: AlarmConfig = { muted: false, down: '', degraded: '' }

export function usePageAlarm(
  monitors: ReadonlyArray<{ id: number; status: Status }>,
  ready: boolean,
  storageKey: string,
  config: AlarmConfig = DEFAULT,
): PageAlarm {
  const key = `opskest-alarm-muted:${storageKey}`
  const [enabled, setEnabled] = useState<boolean>(() => {
    try {
      const v = localStorage.getItem(key)
      if (v !== null) return v !== 'true'
    } catch { /* storage blocked */ }
    return !config.muted
  })
  const [playing, setPlaying] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const prev = useRef<{ down: Set<number>; degraded: Set<number> } | null>(null)
  const stopTimer = useRef<number>()

  // Follow the page's setting until this viewer chooses for themselves.
  useEffect(() => {
    try { if (localStorage.getItem(key) !== null) return } catch { /* ignore */ }
    setEnabled(!config.muted)
  }, [config.muted, key])

  // First interaction anywhere unlocks audio.
  useEffect(() => {
    if (!blocked) return
    const unlock = () => { unlockAudio().then(ok => { if (ok) setBlocked(false) }) }
    window.addEventListener('pointerdown', unlock)
    window.addEventListener('keydown', unlock)
    return () => {
      window.removeEventListener('pointerdown', unlock)
      window.removeEventListener('keydown', unlock)
    }
  }, [blocked])

  useEffect(() => () => { window.clearTimeout(stopTimer.current); stopCurrentAlarm() }, [])

  useEffect(() => {
    if (!ready) return
    const down = new Set<number>()
    const degraded = new Set<number>()
    for (const m of monitors) {
      if (m.status === 'down') down.add(m.id)
      else if (m.status === 'degraded') degraded.add(m.id)
    }
    const before = prev.current
    prev.current = { down, degraded }
    if (!before || !enabled) return

    const newDown = [...down].some(id => !before.down.has(id))
    const newDegraded = [...degraded].some(id => !before.degraded.has(id) && !before.down.has(id))
    if (!newDown && !newDegraded) return

    if (!audioReady()) setBlocked(true)
    const seconds = newDown ? 10 : 5
    if (newDown) playDownAlarm(config.down || 'police-siren', seconds)
    else playDegradedAlarm(config.degraded || 'modern-chime', seconds)
    setPlaying(true)
    window.clearTimeout(stopTimer.current)
    stopTimer.current = window.setTimeout(() => setPlaying(false), seconds * 1000)
  }, [monitors, ready, enabled, config.down, config.degraded])

  // Check once up front, so a board opened on a TV says it's silent before
  // anything fails rather than after.
  useEffect(() => {
    if (enabled && !audioReady()) setBlocked(true)
  }, [enabled])

  const toggle = useCallback(() => {
    if (playing) {
      stopCurrentAlarm()
      window.clearTimeout(stopTimer.current)
      setPlaying(false)
      return // silencing a playing alarm doesn't mute future ones
    }
    unlockAudio().then(ok => { if (ok) setBlocked(false) })
    setEnabled(e => {
      const next = !e
      try { localStorage.setItem(key, next ? 'false' : 'true') } catch { /* ignore */ }
      return next
    })
  }, [playing, key])

  return { enabled, playing, blocked: blocked && enabled, toggle }
}
