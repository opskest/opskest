import { lazy, Suspense, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { Redirect, Route, Switch, useLocation } from 'wouter'
import { PageShell } from './components/PageShell'
import { EmptyState, FullScreenSpinner, ToastProvider } from './components/ui'
import { announceLogin, announceLogout, subscribeAuthChannel } from './lib/authChannel'
import { useMe } from './hooks/useMe'
import { api } from './lib/api'
import { useLive } from './lib/live'
import { canWrite, isAdmin } from './lib/roles'
import type { Me, MonitorSummary } from './lib/types'
const Login = lazy(() => import('./pages/Login'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const Monitors = lazy(() => import('./pages/Monitors'))
const MonitorForm = lazy(() => import('./pages/MonitorForm'))
const Incidents = lazy(() => import('./pages/Incidents'))
const IncidentDetail = lazy(() => import('./pages/IncidentDetail'))
const Projects = lazy(() => import('./pages/Projects'))
const Overview = lazy(() => import('./pages/Overview'))
import { CommandPalette } from './components/CommandPalette'
const Maintenance = lazy(() => import('./pages/Maintenance'))
const StatusPage = lazy(() => import('./pages/StatusPage'))
const StatusHistory = lazy(() => import('./pages/StatusSubpages').then(m => ({ default: m.StatusHistory })))
const StatusIncident = lazy(() => import('./pages/StatusSubpages').then(m => ({ default: m.StatusIncident })))
const StatusConfirmSubscription = lazy(() => import('./pages/StatusSubpages').then(m => ({ default: m.StatusConfirmSubscription })))
const StatusUnsubscribe = lazy(() => import('./pages/StatusSubpages').then(m => ({ default: m.StatusUnsubscribe })))
const PagesAdmin = lazy(() => import('./pages/PagesAdmin'))
const PageEditor = lazy(() => import('./pages/PageEditor'))
const Settings = lazy(() => import('./pages/Settings'))
const AccountSecurity = lazy(() => import('./pages/AccountSecurity'))
const ThirdPartyProviders = lazy(() => import('./pages/ThirdPartyProviders'))

/** Admin-only routes: Settings and team management. */
function AdminGate({ user, children }: { user: Me; children: ReactNode }) {
  if (!isAdmin(user)) return <Redirect to="/" />
  return <>{children}</>
}

/** Routes that create or edit things: admin or user. Viewers go home. */
function UserGate({ user, children }: { user: Me; children: ReactNode }) {
  if (!canWrite(user)) return <Redirect to="/" />
  return <>{children}</>
}

function countDown(monitors: Map<number, MonitorSummary>): number {
  let n = 0
  for (const m of monitors.values()) if (m.status === 'down' && !m.vendor) n++
  return n
}

/** Step 4: title/favicon reflect how many monitors are currently DOWN. */
function useDownIndicator(monitors: Map<number, MonitorSummary>) {
  const downCount = useMemo(() => countDown(monitors), [monitors])

  useEffect(() => {
    document.title = downCount > 0 ? `(${downCount}) Opskest` : 'Opskest'
    const favicon = document.getElementById('favicon') as HTMLLinkElement | null
    if (favicon) favicon.href = downCount > 0 ? '/favicon-down.svg' : '/favicon.svg'
  }, [downCount])
}

function FullApp({ user, onLogout, onMeChanged, afterUnlock }: { user: Me; onLogout: () => void; onMeChanged: () => Promise<unknown>; afterUnlock: boolean }) {
  const [, navigate] = useLocation()
  const live = useLive()
  useDownIndicator(live.monitors)

  // Tell any other open tab that this tab has a session.
  useEffect(() => { announceLogin() }, [])
  // Two-factor was just set up (the shell was locked to Account security): continue to the overview.
  useEffect(() => { if (afterUnlock) navigate('/', { replace: true }) }, [afterUnlock, navigate])

  return (
    <>
    <CommandPalette user={user} />
    <Switch>
      {/* The overview is home; the monitor inventory has its own page. */}
      <Route path="/">
        {() => (
          <PageShell user={user} onLogout={onLogout} fit>
            <Overview user={user} />
          </PageShell>
        )}
      </Route>
      <Route path="/overview"><Redirect to="/" replace /></Route>

      <Route path="/monitors">
        {() => (
          <PageShell user={user} onLogout={onLogout}>
            <Monitors user={user} />
          </PageShell>
        )}
      </Route>

      <Route path="/monitors/new">
        {() => (
          <UserGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <MonitorForm />
            </PageShell>
          </UserGate>
        )}
      </Route>

      <Route path="/monitors/:id/edit">
        {params => (
          <UserGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <MonitorForm id={params.id} />
            </PageShell>
          </UserGate>
        )}
      </Route>

      <Route path="/monitors/:id">
        {params => (
          <PageShell user={user} onLogout={onLogout}>
            <Dashboard user={user} selectedId={Number(params.id)} />
          </PageShell>
        )}
      </Route>

      <Route path="/projects"><PageShell user={user} onLogout={onLogout}><Projects user={user} /></PageShell></Route>
      <Route path="/projects/:id">{params => <PageShell user={user} onLogout={onLogout}><Dashboard user={user} selectedGroupId={Number(params.id)} /></PageShell>}</Route>
      <Route path="/groups/:id">
        {params => (
          <PageShell user={user} onLogout={onLogout}>
            <Dashboard user={user} selectedGroupId={Number(params.id)} />
          </PageShell>
        )}
      </Route>

      <Route path="/wallboard">
        {() => <Redirect to="/monitors" />}
      </Route>

      <Route path="/third-party">
        {() => (
          <PageShell user={user} onLogout={onLogout}>
            <ThirdPartyProviders user={user} />
          </PageShell>
        )}
      </Route>


      {/* Response times and SLA reports live on each monitor's and group's
          page now; old bookmarks land on the dashboard. */}
      <Route path="/reports"><Redirect to="/" replace /></Route>
      <Route path="/latency"><Redirect to="/" replace /></Route>

      <Route path="/incidents/:id">
        {(params: { id: string }) => (
          <PageShell user={user} onLogout={onLogout}>
            <IncidentDetail id={params.id} user={user} />
          </PageShell>
        )}
      </Route>

      <Route path="/incidents">
        {() => (
          <PageShell user={user} onLogout={onLogout}>
            <Incidents user={user} />
          </PageShell>
        )}
      </Route>

      <Route path="/maintenance">
        {() => (
          <PageShell user={user} onLogout={onLogout}>
            <Maintenance user={user} />
          </PageShell>
        )}
      </Route>

      <Route path="/pages/new">
        {() => (
          <UserGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <PageEditor />
            </PageShell>
          </UserGate>
        )}
      </Route>

      <Route path="/pages/:id">
        {params => (
          <UserGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <PageEditor id={params.id} />
            </PageShell>
          </UserGate>
        )}
      </Route>

      <Route path="/pages">
        {() => (
          <UserGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <PagesAdmin />
            </PageShell>
          </UserGate>
        )}
      </Route>

      <Route path="/account/profile"><Redirect to="/account/security" replace /></Route>
      <Route path="/account/security">
        <PageShell user={user} onLogout={onLogout}><AccountSecurity user={user} onMeChanged={onMeChanged} /></PageShell>
      </Route>

      {/* Workspace settings require an administrator. */}
      <Route path="/settings">
        {() => (
          <AdminGate user={user}>
            <PageShell user={user} onLogout={onLogout}>
              <Settings user={user} />
            </PageShell>
          </AdminGate>
        )}
      </Route>

      <Route>
        {() => (
          <PageShell user={user} onLogout={onLogout}>
            <EmptyState heading title="Not found">This page doesn't exist.</EmptyState>
          </PageShell>
        )}
      </Route>
    </Switch>
    </>
  )
}

/**
 * The workspace requires two-factor and this account has none: the only page is Account security, every other
 * path lands there, and the live stream and search stay closed until the authenticator is set up.
 */
function TwoFactorGate({ user, onLogout, onMeChanged }: { user: Me; onLogout: () => void; onMeChanged: () => Promise<unknown> }) {
  return (
    <Switch>
      <Route path="/account/security">
        <PageShell user={user} onLogout={onLogout}><AccountSecurity user={user} onMeChanged={onMeChanged} /></PageShell>
      </Route>
      <Route><Redirect to="/account/security" replace /></Route>
    </Switch>
  )
}

function AuthenticatedApp(props: { user: Me; onLogout: () => void; onMeChanged: () => Promise<unknown> }) {
  const wasLocked = useRef(false)
  if (props.user.twoFactorRequired) wasLocked.current = true
  return props.user.twoFactorRequired ? <TwoFactorGate {...props} /> : <FullApp {...props} afterUnlock={wasLocked.current} />
}

// Everything that needs a session — mounted by the outer Switch below for any path that isn't /status/*.
function Gated() {
  const { user, loading, refresh } = useMe()

  function logout() {
    api.post('/api/auth/logout')
      .catch(() => { /* best-effort: redirect regardless */ })
      .finally(() => {
        announceLogout()
        window.location.href = '/login'
      })
  }

  return (
    <Switch>
      <Route path="/login">
        {() => {
          if (loading) return <FullScreenSpinner />
          return user ? <Redirect to="/" /> : <Login />
        }}
      </Route>

      <Route>
        {() => {
          if (loading) return <FullScreenSpinner />
          if (!user) return <Redirect to="/login" />
          return <AuthenticatedApp user={user} onLogout={logout} onMeChanged={refresh} />
        }}
      </Route>
    </Switch>
  )
}

function AppRoutes() {
  useEffect(() => subscribeAuthChannel(), [])

  return (
    <Switch>
      {/* Public/Private status pages: matched first, before Gated mounts */}
      {/* /status/:slug is always the list view, /status/:slug/board always the board. */}
      <Route path="/status/:slug/subscribe/confirm">
        {params => <StatusConfirmSubscription slug={params.slug} />}
      </Route>
      <Route path="/status/:slug/unsubscribe">
        {params => <StatusUnsubscribe slug={params.slug} />}
      </Route>
      <Route path="/status/:slug/history">
        {params => <StatusHistory slug={params.slug} />}
      </Route>
      <Route path="/status/:slug/incidents/:id">
        {params => <StatusIncident slug={params.slug} id={params.id} />}
      </Route>
      <Route path="/status/:slug/board">
        {params => <StatusPage slug={params.slug} board />}
      </Route>
      <Route path="/status/:slug">
        {params => <StatusPage slug={params.slug} />}
      </Route>

      <Route>{() => <Gated />}</Route>
    </Switch>
  )
}

// Pages load on demand (code-split): a public status page never downloads
// the admin app, and each admin page loads its own code the first time.
export default function App() {
  return (
    <ToastProvider>
      <Suspense fallback={<FullScreenSpinner />}>
        <AppRoutes />
      </Suspense>
    </ToastProvider>
  )
}
