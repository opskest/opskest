export type Theme = 'light' | 'dark' | 'system'

export const THEME_STORAGE_KEY = 'opskest-theme'
export const DEFAULT_THEME: Theme = 'dark'

export function normalizeTheme(value: string | null): Theme {
  return value === 'light' || value === 'dark' || value === 'system' ? value : DEFAULT_THEME
}

function readSavedTheme(): Theme {
  try { return normalizeTheme(window.localStorage.getItem(THEME_STORAGE_KEY)) }
  catch { return DEFAULT_THEME }
}

let currentTheme: Theme | undefined
let listening = false
const listeners = new Set<() => void>()

export function getTheme(): Theme {
  if (!listening) {
    window.addEventListener('storage', onStorage)
    listening = true
  }
  return currentTheme ??= readSavedTheme()
}

// Temporary status-page overrides change presentation, not the saved preference.
export function applyTheme(theme: Theme): void {
  const root = document.documentElement
  root.classList.remove('dark', 'light')
  if (theme !== 'system') root.classList.add(theme)
}

export function setTheme(theme: Theme): void {
  currentTheme = theme
  applyTheme(theme)
  try { window.localStorage.setItem(THEME_STORAGE_KEY, theme) } catch { /* Session-only preference. */ }
  for (const listener of listeners) listener()
}

function onStorage(event: StorageEvent): void {
  if (event.key !== THEME_STORAGE_KEY && event.key !== null) return
  try { if (event.storageArea && event.storageArea !== window.localStorage) return } catch { return }
  currentTheme = normalizeTheme(event.newValue)
  applyTheme(currentTheme)
  for (const listener of listeners) listener()
}

/** A status page's default appearance for a visitor who never chose one: it
 * applies for as long as the page is open and is never saved. */
export function applyDefaultTheme(theme: Theme): () => void {
  let saved: string | null = null
  try { saved = window.localStorage.getItem(THEME_STORAGE_KEY) } catch { /* no storage: nothing is saved */ }
  if (saved !== null) return () => {}
  getTheme()
  currentTheme = theme
  applyTheme(theme)
  for (const listener of listeners) listener()
  return () => {
    currentTheme = undefined
    applyTheme(getTheme())
    for (const listener of listeners) listener()
  }
}

export function subscribeTheme(listener: () => void): () => void {
  getTheme()
  listeners.add(listener)
  // The one storage listener lives for the app lifetime so preferences also
  // update while a route has no mounted appearance control.
  return () => { listeners.delete(listener) }
}
