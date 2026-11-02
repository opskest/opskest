// The real-time store: one EventSource per stream URL, shared and
// reference-counted across every component that asks for it.
//
// Wire protocol: a `snapshot` event replaces the whole state, a
// `delta` event merges changed monitors and drops removed ids. Unchanged
// monitors keep their object identity across deltas, so memoised rows only
// re-render when their own monitor changed. When a reconnect's snapshot
// carries a different server version than the first one, the page reloads
// to pick up the new assets.

import { useEffect, useState } from 'react'
import { normGroup, normOutage } from './normalize'
import type { Cluster, Group, Maintenance, MonitorSummary, Outage } from './types'

export interface Connectivity {
  online: boolean
  since: number
}

export interface LiveState {
  monitors: Map<number, MonitorSummary>
  groups: Group[]
  /** Active outages. */
  outages: Outage[]
  /** Active maintenance windows. */
  maintenance: Maintenance[]
  clusters: Cluster[]
  connectivity: Connectivity
  connected: boolean
  /** Local time (ms) the last snapshot/delta arrived; 0 = nothing yet. */
  lastUpdate: number
}

interface Payload {
  version?: string
  monitors?: MonitorSummary[]
  removed?: number[]
  groups?: unknown[]
  outages?: unknown[]
  maintenance?: Maintenance[]
  clusters?: Cluster[]
  system?: { online?: boolean; since?: number; connectivity?: Connectivity }
}

const RECONNECT_DELAYS_MS = [1000, 2000, 5000, 10000]
const HIDDEN_GRACE_MS = 30_000

function initialState(): LiveState {
  return {
    monitors: new Map(),
    groups: [],
    outages: [],
    maintenance: [],
    clusters: [],
    connectivity: { online: true, since: 0 },
    connected: false,
    lastUpdate: 0,
  }
}

function connectivityOf(system: Payload['system'], prev: Connectivity): Connectivity {
  if (!system) return prev
  const c = system.connectivity ?? system
  return { online: c.online !== false, since: typeof c.since === 'number' ? c.since : prev.since }
}

function parse(e: MessageEvent): Payload | null {
  try {
    return JSON.parse(e.data) as Payload
  } catch {
    return null
  }
}

type Listener = (state: LiveState) => void

class LiveConnection {
  private readonly url: string
  private es: EventSource | null = null
  private reconnectTimer: number | null = null
  private backoffIdx = 0
  private listeners = new Set<Listener>()
  private stopped = false
  private version: string | null = null
  private idleTimer: number | null = null
  private idle = false

  state: LiveState = initialState()

  constructor(url: string) {
    this.url = url
  }

  get refCount(): number {
    return this.listeners.size
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    if (this.listeners.size === 1) {
      this.stopped = false
      document.addEventListener('visibilitychange', this.onVisibility)
      this.open()
    }
    return () => {
      this.listeners.delete(listener)
      if (this.listeners.size === 0) this.close()
    }
  }

  // A browser keeps at most six connections per site over HTTP/1.1, and a
  // stream holds one for as long as it is open. A tab that has been in the
  // background for a while lets its stream go, so a dozen open tabs cannot
  // starve a page that is loading; coming back reconnects and the first
  // message is a fresh snapshot.
  private readonly onVisibility = () => {
    if (document.hidden) {
      if (this.idleTimer == null) this.idleTimer = window.setTimeout(() => this.sleep(), HIDDEN_GRACE_MS)
      return
    }
    if (this.idleTimer != null) { window.clearTimeout(this.idleTimer); this.idleTimer = null }
    if (this.idle) {
      this.idle = false
      this.backoffIdx = 0
      if (!this.stopped && this.listeners.size > 0 && !this.es) this.open()
    }
  }

  private sleep() {
    this.idleTimer = null
    this.idle = true
    this.es?.close()
    this.es = null
    if (this.reconnectTimer != null) { window.clearTimeout(this.reconnectTimer); this.reconnectTimer = null }
  }

  private setState(patch: Partial<LiveState>) {
    this.state = { ...this.state, ...patch }
    for (const listener of this.listeners) listener(this.state)
  }

  private open() {
    const es = new EventSource(this.url, { withCredentials: true })
    this.es = es

    es.addEventListener('snapshot', e => {
      const p = parse(e as MessageEvent)
      if (!p) return
      if (p.version) {
        if (this.version && this.version !== p.version) {
          window.location.reload()
          return
        }
        this.version = p.version
      }
      const monitors = new Map<number, MonitorSummary>()
      for (const m of p.monitors ?? []) monitors.set(m.id, m)
      this.setState({
        monitors,
        groups: (p.groups ?? []).map(normGroup),
        outages: (p.outages ?? []).map(normOutage),
        maintenance: p.maintenance ?? [],
        clusters: p.clusters ?? [],
        connectivity: connectivityOf(p.system, this.state.connectivity),
        connected: true,
        lastUpdate: Date.now(),
      })
    })

    es.addEventListener('delta', e => {
      const p = parse(e as MessageEvent)
      if (!p) return
      let monitors = this.state.monitors
      if ((p.monitors && p.monitors.length > 0) || (p.removed && p.removed.length > 0)) {
        monitors = new Map(monitors)
        for (const m of p.monitors ?? []) monitors.set(m.id, m)
        for (const id of p.removed ?? []) monitors.delete(id)
      }
      this.setState({
        monitors,
        groups: p.groups ? p.groups.map(normGroup) : this.state.groups,
        outages: p.outages ? p.outages.map(normOutage) : this.state.outages,
        maintenance: p.maintenance ?? this.state.maintenance,
        clusters: p.clusters ?? this.state.clusters,
        connectivity: connectivityOf(p.system, this.state.connectivity),
        connected: true,
        lastUpdate: Date.now(),
      })
    })

    // The server build changed; a full reload picks up the new asset hashes.
    es.addEventListener('version', () => window.location.reload())

    es.onopen = () => {
      this.backoffIdx = 0
    }

    es.onerror = () => {
      es.close()
      if (this.es === es) this.es = null
      if (this.state.connected) this.setState({ connected: false })
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect() {
    if (this.stopped || this.idle || this.reconnectTimer != null) return
    const delay = RECONNECT_DELAYS_MS[Math.min(this.backoffIdx, RECONNECT_DELAYS_MS.length - 1)]
    this.backoffIdx++
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      if (!this.stopped && !this.idle && this.listeners.size > 0) this.open()
    }, delay)
  }

  private close() {
    this.stopped = true
    document.removeEventListener('visibilitychange', this.onVisibility)
    if (this.idleTimer != null) { window.clearTimeout(this.idleTimer); this.idleTimer = null }
    this.idle = false
    this.es?.close()
    this.es = null
    if (this.reconnectTimer != null) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }
}

const connections = new Map<string, LiveConnection>()

function getConnection(url: string): LiveConnection {
  let conn = connections.get(url)
  if (!conn) {
    conn = new LiveConnection(url)
    connections.set(url, conn)
  }
  return conn
}

function useLiveConnection(url: string): LiveState {
  const [state, setState] = useState<LiveState>(() => getConnection(url).state)

  useEffect(() => {
    const conn = getConnection(url)
    setState(conn.state)
    const unsubscribe = conn.subscribe(setState)
    return () => {
      unsubscribe()
      // Drop the connection once the last subscriber leaves, so a later
      // mount starts clean with a fresh snapshot.
      const c = connections.get(url)
      if (c && c.refCount === 0) connections.delete(url)
    }
  }, [url])

  return state
}

/** The main app's live store: every monitor, group, outage and cluster. */
export function useLive(): LiveState {
  return useLiveConnection('/api/stream')
}

/** Same protocol, scoped to one status page's stream. */
export function useLiveScoped(url: string): LiveState {
  return useLiveConnection(url)
}
