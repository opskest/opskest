// Cross-tab auth sync over the `opskest_auth` BroadcastChannel (Global
// Constraints; spec §13). Without this, signing out in one tab leaves
// every other tab sitting on a page that will 401 on its next request, and
// signing in in a popup/second tab leaves the first tab on /login until it
// happens to refetch.
//
// BroadcastChannel isn't available in every browsing context (older
// Safari, some privacy modes), so every entry point here is a no-op if the
// constructor throws or is missing — cross-tab sync is a nicety, not a
// requirement for the app to work in a single tab.

export type AuthMessage = { type: 'login' } | { type: 'logout' }

const CHANNEL_NAME = 'opskest_auth'

/** True for /status and /status/... — pages that must work without a session. */
export function isStatusPage(pathname: string): boolean {
  return pathname === '/status' || pathname.startsWith('/status/')
}

function openChannel(): BroadcastChannel | null {
  try {
    return typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(CHANNEL_NAME) : null
  } catch {
    return null
  }
}

function post(message: AuthMessage): void {
  const ch = openChannel()
  if (!ch) return
  try {
    ch.postMessage(message)
  } catch {
    // best effort
  } finally {
    ch.close()
  }
}

/** Call right before navigating away after a successful sign-out. */
export function announceLogout(): void {
  post({ type: 'logout' })
}

/** Call once, when a tab discovers it has an authenticated session. */
export function announceLogin(): void {
  post({ type: 'login' })
}

/**
 * Subscribes this tab to auth events broadcast by other tabs. Returns an
 * unsubscribe function; safe to call even where BroadcastChannel doesn't
 * exist (returns a no-op).
 *
 * - `logout` elsewhere sends this tab to /login too — unless it's already
 *   there, or it's showing a status page (§11 pages must keep working
 *   signed-out; Task 17 gives a private one its own "sign in" prompt
 *   instead of a hard redirect).
 * - `login` elsewhere reloads this tab only if it's sitting on /login, so
 *   it picks up the new session instead of leaving the user to sign in
 *   twice.
 */
export function subscribeAuthChannel(): () => void {
  const ch = openChannel()
  if (!ch) return () => {}

  const onMessage = (e: MessageEvent<AuthMessage>) => {
    const path = window.location.pathname
    if (e.data?.type === 'logout') {
      if (path !== '/login' && !isStatusPage(path)) window.location.href = '/login'
    } else if (e.data?.type === 'login') {
      if (path === '/login') window.location.reload()
    }
  }

  ch.addEventListener('message', onMessage)
  return () => {
    ch.removeEventListener('message', onMessage)
    try { ch.close() } catch { /* already closed */ }
  }
}
