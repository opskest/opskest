import { useEffect, useState } from 'react'

/** 'light' | 'dark' as actually rendered right now: an explicit .dark/.light
 * stamp on <html> wins, otherwise the OS preference. Watches both, so a
 * toggle anywhere (or the wallboard's ?theme=) restyles canvas charts and
 * board cards and theme controls at once. */
export function useResolvedTheme(): 'light' | 'dark' {
  const read = (): 'light' | 'dark' => {
    const c = document.documentElement.classList
    if (c.contains('dark')) return 'dark'
    if (c.contains('light')) return 'light'
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  const [theme, setTheme] = useState(read)

  useEffect(() => {
    const update = () => setTheme(read())
    const mo = new MutationObserver(update)
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', update)
    update()
    return () => {
      mo.disconnect()
      mq.removeEventListener('change', update)
    }
  }, [])

  return theme
}
