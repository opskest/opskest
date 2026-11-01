export interface RuntimeSettings { timeZone: string; serverRegion: string }
let settings: RuntimeSettings = { timeZone: 'UTC', serverRegion: 'This server' }

export function configureRuntime(value: Partial<RuntimeSettings>): void {
  let timeZone = 'UTC'
  if (typeof value.timeZone === 'string') {
    try { timeZone = new Intl.DateTimeFormat('en', { timeZone: value.timeZone }).resolvedOptions().timeZone } catch { /* Use the portable fallback. */ }
  }
  settings = { timeZone, serverRegion: typeof value.serverRegion === 'string' && value.serverRegion.trim() ? value.serverRegion.trim() : 'This server' }
}
export function serverTimeZone(): string { return settings.timeZone }
export function serverRegion(): string { return settings.serverRegion }

/** Load installation context before any timezone-dependent view renders. */
export async function loadRuntime(): Promise<void> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 4000)
  try {
    const response = await fetch('/api/runtime', { signal: controller.signal, credentials: 'same-origin' })
    if (response.ok) configureRuntime(await response.json())
  } catch { /* An older or unavailable server gets neutral UTC defaults. */ }
  finally { window.clearTimeout(timeout) }
}
