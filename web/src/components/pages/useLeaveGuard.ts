// Warns before the editor is left with unsaved changes: closing or reloading
// the tab (the browser's own prompt) and following any in-app link (a dialog
// the caller shows, from `target`). Links that stay inside the editor, and
// links that open elsewhere, are let through.

import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'wouter'

export function useLeaveGuard(dirty: boolean, stays: (url: URL) => boolean = () => false) {
  const [, navigate] = useLocation()
  const [target, setTarget] = useState<string | null>(null)
  const staysRef = useRef(stays)
  staysRef.current = stays

  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    const onClick = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null
      if (!a || (a.target && a.target !== '_self') || a.hasAttribute('download')) return
      const url = new URL(a.href, window.location.href)
      if (url.origin !== window.location.origin || staysRef.current(url)) return
      e.preventDefault()
      e.stopPropagation()
      setTarget(url.pathname + url.search + url.hash)
    }
    window.addEventListener('beforeunload', warn)
    document.addEventListener('click', onClick, true)
    return () => {
      window.removeEventListener('beforeunload', warn)
      document.removeEventListener('click', onClick, true)
    }
  }, [dirty])

  return {
    /** Where the visitor tried to go, while the confirmation is open. */
    target,
    /** Ask before going to `path` (used by the Cancel button). */
    ask: (path: string) => (dirty ? setTarget(path) : navigate(path)),
    stay: () => setTarget(null),
    leave: () => { const t = target; setTarget(null); if (t) navigate(t) },
  }
}
