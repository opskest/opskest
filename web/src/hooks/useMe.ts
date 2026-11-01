import { useCallback, useEffect, useState } from 'react'
import { ApiError, TWO_FACTOR_REQUIRED_EVENT, UNAUTHENTICATED_EVENT, api } from '../lib/api'
import { isStatusPage } from '../lib/authChannel'
import type { Me } from '../lib/types'

// GET /api/me. Sessions can expire during use, so this hook redirects to
// /login once on the initial 401,
// and again whenever any other api.ts call reports a 401 via the
// `opskest:unauthenticated` window event (dispatched from lib/api.ts).
//
// Status pages (/status/:slug(/board)?) never call this hook — App.tsx
// only mounts it under the authenticated part of the route tree — but
// goToLogin still refuses to fire on one, as a second line of defence:
// they must keep working for a signed-out visitor (spec §11), and a
// private one shows its own "sign in" prompt (Task 17) rather than being
// yanked to /login.
export function useMe() {
  const [user, setUser] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)

  const goToLogin = useCallback(() => {
    const path = window.location.pathname
    if (path === '/login' || isStatusPage(path)) return
    const next = `${path}${window.location.search}`
    window.location.href = next && next !== '/' ? `/login?next=${encodeURIComponent(next)}` : '/login'
  }, [])

  // The first load shows the spinner; later refreshes (after two-factor is
  // set up, or a request was refused for lack of it) swap the account in place.
  const load = useCallback((initial: boolean) => {
    if (initial) setLoading(true)
    return api.get<Me>('/api/me')
      .then(me => setUser(me))
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) { setUser(null); goToLogin() }
        else if (initial) setUser(null)
      })
      .finally(() => { if (initial) setLoading(false) })
  }, [goToLogin])
  const refresh = useCallback(() => load(false), [load])

  useEffect(() => { void load(true) }, [load])

  useEffect(() => {
    function onTwoFactorRequired() { void load(false) }
    window.addEventListener(TWO_FACTOR_REQUIRED_EVENT, onTwoFactorRequired)
    return () => window.removeEventListener(TWO_FACTOR_REQUIRED_EVENT, onTwoFactorRequired)
  }, [load])

  useEffect(() => {
    function onUnauthenticated() {
      setUser(null)
      goToLogin()
    }
    window.addEventListener(UNAUTHENTICATED_EVENT, onUnauthenticated)
    return () => window.removeEventListener(UNAUTHENTICATED_EVENT, onUnauthenticated)
  }, [goToLogin])

  return { user, loading, refresh }
}
