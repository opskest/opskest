// Web Audio API & Speech Synthesis sound engine for status page alarms.
// Zero external asset dependencies — 100% offline, guaranteed zero 404s.

export type HeavySoundType = 'police-siren' | 'industrial-klaxon' | 'retro-panic'
export type LightSoundType = 'modern-chime' | 'tech-radar'
export type SoundType = HeavySoundType | LightSoundType

export interface SoundOption {
  id: HeavySoundType | LightSoundType
  label: string
  desc: string
  category: 'heavy' | 'light'
}

export const HEAVY_SOUND_OPTIONS: SoundOption[] = [
  { id: 'police-siren', label: 'Siren', desc: 'Urgent two-tone wailing emergency siren', category: 'heavy' },
  { id: 'industrial-klaxon', label: 'Klaxon', desc: 'Deep pulsating industrial emergency horn', category: 'heavy' },
  { id: 'retro-panic', label: 'Rapid beep', desc: '8-bit arcade fast panic alarm', category: 'heavy' },
]

export const LIGHT_SOUND_OPTIONS: SoundOption[] = [
  { id: 'modern-chime', label: 'Chime', desc: 'Gentle, crisp two-tone alert chime', category: 'light' },
  { id: 'tech-radar', label: 'Pulse', desc: 'High-frequency telemetry beep pulse', category: 'light' },
]

export const ALL_SOUND_OPTIONS = [...HEAVY_SOUND_OPTIONS, ...LIGHT_SOUND_OPTIONS]

let audioCtx: AudioContext | null = null
let currentAlarmNodes: { stop: () => void }[] = []

function getAudioContext(): AudioContext | null {
  if (typeof window === 'undefined') return null
  const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext
  if (!AudioContextClass) return null
  if (!audioCtx) {
    audioCtx = new AudioContextClass()
  }
  if (audioCtx.state === 'suspended') {
    audioCtx.resume().catch(() => {})
  }
  return audioCtx
}

/** True once the browser lets this page play sound (after a user gesture). */
export function audioReady(): boolean {
  const ctx = getAudioContext()
  return !!ctx && ctx.state === 'running'
}

/** Resume audio from inside a user gesture; resolves whether it's now allowed. */
export async function unlockAudio(): Promise<boolean> {
  const ctx = getAudioContext()
  if (!ctx) return false
  try { await ctx.resume() } catch { return false }
  return ctx.state === 'running'
}

export function stopCurrentAlarm() {
  for (const n of currentAlarmNodes) {
    try { n.stop() } catch {}
  }
  currentAlarmNodes = []
}

/**
 * Plays HEAVY DOWN alarm for 10 seconds:
 * - Police Siren
 * - Industrial Klaxon
 * - Retro Panic
 */
export function playDownAlarm(type: HeavySoundType = 'police-siren', durationSeconds = 10, volume = 0.5) {
  stopCurrentAlarm()

  const ctx = getAudioContext()
  if (!ctx) return

  const now = ctx.currentTime
  const masterGain = ctx.createGain()
  masterGain.gain.setValueAtTime(Math.max(0.01, Math.min(1, volume)), now)
  masterGain.connect(ctx.destination)

  switch (type) {
    case 'police-siren': {
      // Police Siren: sweeping sawtooth wail over durationSeconds
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()

      osc.type = 'sawtooth'
      const cycleDuration = 0.8
      const cycles = Math.ceil(durationSeconds / cycleDuration)

      for (let i = 0; i < cycles; i++) {
        const start = now + i * cycleDuration
        osc.frequency.setValueAtTime(450, start)
        osc.frequency.exponentialRampToValueAtTime(1050, start + cycleDuration * 0.5)
        osc.frequency.exponentialRampToValueAtTime(450, start + cycleDuration)
      }

      const filter = ctx.createBiquadFilter()
      filter.type = 'lowpass'
      filter.frequency.setValueAtTime(1800, now)

      gain.gain.setValueAtTime(0.01, now)
      gain.gain.linearRampToValueAtTime(0.4, now + 0.1)
      gain.gain.setValueAtTime(0.4, now + durationSeconds - 0.3)
      gain.gain.exponentialRampToValueAtTime(0.001, now + durationSeconds)

      osc.connect(filter)
      filter.connect(gain)
      gain.connect(masterGain)

      osc.start(now)
      osc.stop(now + durationSeconds)

      currentAlarmNodes.push({
        stop: () => { try { osc.stop() } catch {} },
      })
      break
    }

    case 'industrial-klaxon': {
      for (const freq of [180, 225]) {
        const osc = ctx.createOscillator()
        const gain = ctx.createGain()
        osc.type = 'square'
        osc.frequency.setValueAtTime(freq, now)

        const pulseDuration = 0.7
        const pulses = Math.ceil(durationSeconds / pulseDuration)
        for (let i = 0; i < pulses; i++) {
          const t = now + i * pulseDuration
          gain.gain.setValueAtTime(0.001, t)
          gain.gain.linearRampToValueAtTime(0.3, t + 0.05)
          gain.gain.setValueAtTime(0.3, t + 0.45)
          gain.gain.exponentialRampToValueAtTime(0.001, t + 0.65)
        }

        osc.connect(gain)
        gain.connect(masterGain)
        osc.start(now)
        osc.stop(now + durationSeconds)

        currentAlarmNodes.push({
          stop: () => { try { osc.stop() } catch {} },
        })
      }
      break
    }

    case 'retro-panic': {
      const freqs = [350, 440, 523, 660, 880, 1046]
      const noteLen = 0.08
      const arpeggioLen = freqs.length * noteLen
      const loops = Math.ceil(durationSeconds / arpeggioLen)

      for (let l = 0; l < loops; l++) {
        freqs.forEach((freq, idx) => {
          const noteStart = now + l * arpeggioLen + idx * noteLen
          if (noteStart >= now + durationSeconds) return

          const osc = ctx.createOscillator()
          const gain = ctx.createGain()
          osc.type = 'triangle'
          osc.frequency.setValueAtTime(freq, noteStart)

          gain.gain.setValueAtTime(0.25, noteStart)
          gain.gain.exponentialRampToValueAtTime(0.001, noteStart + noteLen * 0.9)

          osc.connect(gain)
          gain.connect(masterGain)
          osc.start(noteStart)
          osc.stop(noteStart + noteLen)

          currentAlarmNodes.push({
            stop: () => { try { osc.stop() } catch {} },
          })
        })
      }
      break
    }
  }
}

/**
 * Plays LIGHT DEGRADED warning sound for 5 seconds:
 * - Modern Chime
 * - Tech Radar
 */
export function playDegradedAlarm(type: LightSoundType = 'modern-chime', durationSeconds = 5, volume = 0.35) {
  stopCurrentAlarm()
  const ctx = getAudioContext()
  if (!ctx) return

  const now = ctx.currentTime
  const masterGain = ctx.createGain()
  masterGain.gain.setValueAtTime(Math.max(0.01, Math.min(1, volume)), now)
  masterGain.connect(ctx.destination)

  switch (type) {
    case 'modern-chime': {
      // Pleasant multi-tone chime repeated over 5 seconds
      const notes = [523.25, 659.25, 783.99, 1046.50] // C5, E5, G5, C6
      const loopLen = 1.2
      const loops = Math.ceil(durationSeconds / loopLen)

      for (let l = 0; l < loops; l++) {
        notes.forEach((freq, idx) => {
          const noteStart = now + l * loopLen + idx * 0.14
          if (noteStart >= now + durationSeconds) return

          const osc = ctx.createOscillator()
          const gain = ctx.createGain()
          osc.type = 'sine'
          osc.frequency.setValueAtTime(freq, noteStart)

          gain.gain.setValueAtTime(0.001, noteStart)
          gain.gain.linearRampToValueAtTime(0.3, noteStart + 0.02)
          gain.gain.exponentialRampToValueAtTime(0.001, noteStart + 0.7)

          osc.connect(gain)
          gain.connect(masterGain)
          osc.start(noteStart)
          osc.stop(noteStart + 0.75)

          currentAlarmNodes.push({
            stop: () => { try { osc.stop() } catch {} },
          })
        })
      }
      break
    }

    case 'tech-radar': {
      // High-frequency telemetry radar pulse
      const beepInterval = 0.5
      const beeps = Math.ceil(durationSeconds / beepInterval)
      for (let i = 0; i < beeps; i++) {
        const start = now + i * beepInterval
        if (start >= now + durationSeconds) break

        const osc = ctx.createOscillator()
        const gain = ctx.createGain()
        osc.type = 'sine'
        osc.frequency.setValueAtTime(1046.50, start) // C6

        gain.gain.setValueAtTime(0.001, start)
        gain.gain.linearRampToValueAtTime(0.3, start + 0.02)
        gain.gain.exponentialRampToValueAtTime(0.001, start + 0.3)

        osc.connect(gain)
        gain.connect(masterGain)
        osc.start(start)
        osc.stop(start + 0.32)

        currentAlarmNodes.push({
          stop: () => { try { osc.stop() } catch {} },
        })
      }
      break
    }
  }
}
