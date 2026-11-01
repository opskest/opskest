import { useSyncExternalStore } from 'react'
import { getTheme, setTheme, subscribeTheme } from '../lib/theme'
import { useResolvedTheme } from './useResolvedTheme'

export { applyTheme, type Theme } from '../lib/theme'

export function useTheme() {
  const theme = useSyncExternalStore(subscribeTheme, getTheme)
  const resolved = useResolvedTheme()
  const toggle = () => setTheme(resolved === 'dark' ? 'light' : 'dark')

  return { theme, resolved, toggle, setTheme }
}
