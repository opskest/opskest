import { useCallback, useEffect, useState } from 'react'
import { api, errorText, unwrapList } from '../../lib/api'
import { dateServer } from '../../lib/format'
import { Button, ConfirmModal, ErrorRetry, Field, Select, useToast } from '../ui'
import { SettingsFooter, SettingsLoading, SettingsSection, SettingsStats, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from './SettingsPanel'

// Settings → Data: how much the database holds, and admin-driven cleanup
// ("clear history, keep the last N days", optionally for one monitor) plus
// an explicit Compact (VACUUM) to hand freed space back to the disk.
// Configuration — monitors, channels, pages, users — is never cleared here.

interface TableUsage { name: string; label: string; rows: number; oldestTs: number | null }
interface UsageResponse {
  usage: { fileBytes: number; walBytes: number; freeBytes: number; tables: TableUsage[] }
  retentionRawDays?: number
}

const KINDS: { id: string; label: string; perMonitor: boolean }[] = [
  { id: 'heartbeats', label: 'Check results (raw heartbeats)', perMonitor: true },
  { id: 'status_events', label: 'Status events', perMonitor: true },
  { id: 'outages', label: 'Resolved outages', perMonitor: true },
  { id: 'alert_log', label: 'Alert delivery log', perMonitor: true },
  { id: 'audit_log', label: 'Activity log', perMonitor: false },
]

const KEEP: { days: number; label: string }[] = [
  { days: 90, label: 'Keep last 90 days' },
  { days: 30, label: 'Keep last 30 days' },
  { days: 7, label: 'Keep last 7 days' },
  { days: 1, label: 'Keep last 24 hours' },
  { days: 0, label: 'Delete everything' },
]

function formatBytes(b: number): string {
  if (!b || b <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(b) / Math.log(1024)))
  return `${(b / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function DataPanel() {
  const toast = useToast()
  const [data, setData] = useState<UsageResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [monitors, setMonitors] = useState<{ id: number; name: string }[]>([])
  const [kind, setKind] = useState('heartbeats')
  const [keepDays, setKeepDays] = useState(30)
  const [monitorId, setMonitorId] = useState<number | ''>('')
  const [confirm, setConfirm] = useState(false)
  const [compacting, setCompacting] = useState(false)

  const load = useCallback(() => {
    setError(null)
    api.get<UsageResponse>('/api/data/usage')
      .then(setData)
      .catch(e => setError(errorText(e, 'Could not load data usage')))
  }, [])
  useEffect(load, [load])
  useEffect(() => {
    api.get<unknown>('/api/monitors')
      .then(r => setMonitors(unwrapList<{ id: number; name: string }>(r, 'monitors')
        .map(m => ({ id: m.id, name: m.name })).sort((a, b) => a.name.localeCompare(b.name))))
      .catch(() => setMonitors([]))
  }, [])

  const kindMeta = KINDS.find(k => k.id === kind)!
  const scopeMonitor = kindMeta.perMonitor && monitorId !== '' ? monitors.find(m => m.id === monitorId) : undefined
  const keepLabel = KEEP.find(k => k.days === keepDays)?.label ?? ''

  async function clear() {
    try {
      const body: Record<string, unknown> = { kind, keepDays }
      if (scopeMonitor) body.monitorId = scopeMonitor.id
      const res = await api.post<{ removed: number }>('/api/data/clear', body)
      toast.push(`Removed ${res.removed.toLocaleString()} rows`, 'ok')
      setConfirm(false)
      load()
    } catch (e) {
      toast.push(errorText(e, 'Clear failed'), 'danger')
      setConfirm(false)
    }
  }

  async function compact() {
    setCompacting(true)
    try {
      const res = await api.post<{ beforeBytes: number; afterBytes: number }>('/api/data/compact')
      const saved = Math.max(0, res.beforeBytes - res.afterBytes)
      toast.push(`Compacted: ${formatBytes(res.beforeBytes)} → ${formatBytes(res.afterBytes)} (${formatBytes(saved)} freed)`, 'ok')
      load()
    } catch (e) {
      toast.push(errorText(e, 'Compact failed'), 'danger')
    } finally {
      setCompacting(false)
    }
  }

  if (error) return <SettingsSection><ErrorRetry message={error} onRetry={load} /></SettingsSection>
  if (!data) return <SettingsLoading label="Loading data usage" sections={3} rows={2} />
  const u = data.usage

  return (
    <>
      <SettingsSection title="Database size" description="What the database file and its write-ahead log take on disk.">
        <SettingsStats items={[
          { label: 'Database file', value: formatBytes(u.fileBytes), hint: 'The main data file' },
          { label: 'Write-ahead log', value: formatBytes(u.walBytes), hint: 'Recent writes not yet merged in' },
          { label: 'Reclaimable', value: formatBytes(u.freeBytes), hint: 'Free space that Compact can return' },
          { label: 'Rows stored', value: u.tables.reduce((n, t) => n + t.rows, 0).toLocaleString(), hint: 'Across every kind of data below' },
        ]} />
      </SettingsSection>

      <SettingsSection title="What is stored" description={data.retentionRawDays != null
        ? `Raw check results are pruned automatically after ${data.retentionRawDays} days (Monitoring rules). Hourly and daily rollups keep charts and uptime history after raw results are cleared.`
        : 'Rows held per kind of data, and the oldest entry of each.'}>
        <SettingsTable>
          <table className={`${ST_TABLE} min-w-[360px]`}>
            <thead className={ST_HEAD}>
              <tr>
                <th className={ST_TH}>Data</th>
                <th className={`${ST_TH} text-right`}>Rows</th>
                <th className={`${ST_TH} text-right`}>Oldest</th>
              </tr>
            </thead>
            <tbody className={ST_BODY}>
              {u.tables.map(t => (
                <tr key={t.name} className={ST_ROW}>
                  <td className={`${ST_TD} text-body`}>{t.label}</td>
                  <td className={`${ST_TD} text-right font-mono text-body-2`}>{t.rows.toLocaleString()}</td>
                  <td className={`${ST_TD} text-right text-muted`}>{t.oldestTs ? dateServer(t.oldestTs) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </SettingsTable>
      </SettingsSection>

      <SettingsSection title="Clear history" description="Deletes history older than the period you keep. Ongoing outages are never removed."
        footer={<SettingsFooter><Button variant="danger" onClick={() => setConfirm(true)}>Clear history</Button></SettingsFooter>}>
        <div className="grid grid-cols-1 items-start gap-x-5 gap-y-5 sm:grid-cols-3">
          <Field label="Data">
            <Select value={kind} onChange={e => setKind(e.target.value)}>
              {KINDS.map(k => <option key={k.id} value={k.id}>{k.label}</option>)}
            </Select>
          </Field>
          <Field label="Keep">
            <Select value={keepDays} onChange={e => setKeepDays(Number(e.target.value))}>
              {KEEP.map(k => <option key={k.days} value={k.days}>{k.label}</option>)}
            </Select>
          </Field>
          <Field label="Monitor">
            <Select value={monitorId} disabled={!kindMeta.perMonitor}
              onChange={e => setMonitorId(e.target.value === '' ? '' : Number(e.target.value))}>
              <option value="">All monitors</option>
              {monitors.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
            </Select>
          </Field>
        </div>
      </SettingsSection>

      <SettingsSection title="Compact database" description="Returns freed space to the disk. Writes pause for a few seconds while it runs."
        actions={<Button variant="secondary" busy={compacting} onClick={compact}>Compact now</Button>} />

      <ConfirmModal
        open={confirm}
        title="Clear history?"
        description={<>
          <b>{kindMeta.label}</b> for <b>{scopeMonitor ? scopeMonitor.name : 'all monitors'}</b>: {keepLabel.toLowerCase()}.
          {' '}This cannot be undone. Take a backup first if you may need it.
        </>}
        confirmLabel="Clear"
        onConfirm={clear}
        onClose={() => setConfirm(false)}
      />
    </>
  )
}
