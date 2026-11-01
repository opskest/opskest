import { useCallback, useEffect, useRef, useState } from 'react'
import { api, errorText } from '../../lib/api'
import { ago, dateServer } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import { Pager, usePaging } from '../Pager'
import { Alert, Button, buttonClass, ConfirmModal, EmptyState, ErrorRetry, LoadingBlock, Badge, useToast } from '../ui'
import { DANGER_GHOST, RowActions, SettingsActions, SettingsLoading, SettingsSection, SettingsStats, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from './SettingsPanel'

// Settings → Backup & Database: list snapshots, take one now, download,
// delete, restore (from a listed file or an uploaded .db). A restore stages
// the file, the server snapshots current data as pre-restore-*.db and
// restarts; we then wait for /healthz and reload.

interface BackupRow { name: string; size: number; createdAt: number }
interface BackupsResponse {
  backups: BackupRow[] | null
  lastRun: { at: number; ok: boolean; error?: string } | null
  schedule: { hour: number; timeZone?: string; keep: number }
}

function formatBytes(b: number): string {
  if (!b || b <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(b) / Math.log(1024)))
  return `${(b / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}

async function waitForRestart(): Promise<void> {
  // Give the old process time to exit, then poll until the new one answers.
  await new Promise(r => setTimeout(r, 2500))
  for (let i = 0; i < 60; i++) {
    try {
      const res = await fetch('/healthz', { cache: 'no-store' })
      if (res.ok) return
    } catch { /* still restarting */ }
    await new Promise(r => setTimeout(r, 1000))
  }
}

export function BackupsPanel() {
  const toast = useToast()
  const [data, setData] = useState<BackupsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busyNow, setBusyNow] = useState(false)
  const [restoreTarget, setRestoreTarget] = useState<BackupRow | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<BackupRow | null>(null)
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const [restarting, setRestarting] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    setError(null)
    api.get<BackupsResponse>('/api/backups')
      .then(setData)
      .catch(e => setError(errorText(e, 'Could not load backups')))
  }, [])
  useEffect(load, [load])

  const backupPaging = usePaging(data?.backups ?? [])

  async function backupNow() {
    setBusyNow(true)
    try {
      await api.post('/api/backups')
      toast.push('Backup created', 'ok')
      load()
    } catch (e) {
      toast.push(errorText(e, 'Backup failed'), 'danger')
    } finally {
      setBusyNow(false)
    }
  }

  async function afterRestoreAccepted() {
    setRestarting(true)
    await waitForRestart()
    window.location.reload()
  }

  async function restoreListed(row: BackupRow) {
    try {
      await api.post(`/api/backups/${encodeURIComponent(row.name)}/restore`)
    } catch (e) {
      toast.push(errorText(e, 'Restore failed'), 'danger')
      setRestoreTarget(null)
      return
    }
    setRestoreTarget(null)
    await afterRestoreAccepted()
  }

  async function restoreUpload(file: File) {
    const form = new FormData()
    form.append('file', file)
    try {
      await api.send('POST', '/api/backups/upload', form)
    } catch (e) {
      toast.push(errorText(e, 'That file could not be restored'), 'danger')
      setUploadFile(null)
      return
    }
    setUploadFile(null)
    await afterRestoreAccepted()
  }

  async function remove(row: BackupRow) {
    try {
      await api.del(`/api/backups/${encodeURIComponent(row.name)}`)
      toast.push('Backup deleted', 'ok')
      load()
    } catch (e) {
      toast.push(errorText(e, 'Delete failed'), 'danger')
    } finally {
      setDeleteTarget(null)
    }
  }

  if (restarting) {
    return (
      <SettingsSection className="py-10 text-center">
        <LoadingBlock label="Restoring" className="py-4" />
        <p className="text-sm font-semibold text-body">Restoring backup and restarting…</p>
        <p className="mt-1 text-xs text-muted">This usually takes a few seconds. The page reloads automatically.</p>
      </SettingsSection>
    )
  }
  // The title-row actions are there while the list loads too, so the row never changes.
  const actions = (
    <SettingsActions>
      <input
        ref={fileInput}
        type="file"
        accept=".db,application/octet-stream,application/x-sqlite3"
        className="hidden"
        onChange={e => { const f = e.target.files?.[0]; if (f) setUploadFile(f); e.target.value = '' }}
      />
      <Button variant="secondary" onClick={() => fileInput.current?.click()}>Upload &amp; restore…</Button>
      <Button variant="primary" busy={busyNow} onClick={backupNow}>Back up now</Button>
    </SettingsActions>
  )
  if (error) return <>{actions}<SettingsSection><ErrorRetry message={error} onRetry={load} /></SettingsSection></>
  if (!data) return <>{actions}<SettingsLoading label="Loading backups" sections={1} rows={4} /></>

  const rows = data.backups ?? []
  const newest = rows.length > 0 ? rows.reduce((a, b) => (b.createdAt > a.createdAt ? b : a)) : null
  const totalBytes = rows.reduce((n, r) => n + r.size, 0)
  const zone = data.schedule.timeZone || serverTimeZone()

  return (
    <>
      {actions}

      <SettingsSection title="Snapshots" description="Nightly and manual database snapshots. Restoring replaces all current data after saving a safety copy.">
        <SettingsStats items={[
          { label: 'Snapshots', value: rows.length, hint: `Keeps the last ${data.schedule.keep}` },
          { label: 'Total size', value: formatBytes(totalBytes), hint: 'On disk in the backup folder' },
          { label: 'Latest snapshot', value: newest ? ago(newest.createdAt) : 'None', hint: newest ? dateServer(newest.createdAt) : 'Nothing backed up yet', mono: false },
          {
            label: 'Last nightly run', mono: false,
            value: data.lastRun ? (data.lastRun.ok ? 'Succeeded' : 'Failed') : 'Not yet',
            tone: data.lastRun ? (data.lastRun.ok ? 'text-ok' : 'text-danger') : undefined,
            hint: data.lastRun ? dateServer(data.lastRun.at) : `Next at ${String(data.schedule.hour).padStart(2, '0')}:00 ${zone}`,
          },
        ]} />
        {data.lastRun && !data.lastRun.ok && data.lastRun.error && (
          <Alert tone="danger"><span className="font-mono text-[12px]">{data.lastRun.error}</span></Alert>
        )}
        {rows.length === 0 ? (
          <EmptyState compact title="No backups yet"
            action={<Button variant="secondary" busy={busyNow} onClick={backupNow}>Back up now</Button>}>
            Take the first snapshot now, or wait for the nightly run.
          </EmptyState>
        ) : (
        <SettingsTable>
          <table className={`${ST_TABLE} min-w-[560px]`}>
            <thead className={ST_HEAD}>
              <tr>
                <th className={ST_TH}>File</th>
                <th className={ST_TH}>Size</th>
                <th className={ST_TH}>Created ({zone})</th>
                <th className={`${ST_TH} text-right`}><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className={ST_BODY}>
              {backupPaging.rows.map(row => (
                <tr key={row.name} className={ST_ROW}>
                  <td className={`${ST_TD} font-mono text-body`}>
                    {row.name}
                    {row.name.startsWith('pre-restore-') && <span className="ml-2"><Badge tone="warn">safety copy</Badge></span>}
                    {newest?.name === row.name && <span className="ml-2"><Badge tone="accent">latest</Badge></span>}
                  </td>
                  <td className={`${ST_TD} text-muted tnum`}>{formatBytes(row.size)}</td>
                  <td className={`${ST_TD} text-muted`}>{dateServer(row.createdAt)}</td>
                  <td className={ST_TD}>
                    <RowActions>
                      <a
                        className={buttonClass('secondary', 'sm')}
                        href={`/api/backups/${encodeURIComponent(row.name)}/download`}
                        download={row.name}
                      >Download</a>
                      <Button size="sm" variant="secondary" onClick={() => setRestoreTarget(row)}>Restore</Button>
                      <Button size="sm" variant="ghost" className={DANGER_GHOST} aria-label={`Delete ${row.name}`} title="Delete" onClick={() => setDeleteTarget(row)}>Delete</Button>
                    </RowActions>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </SettingsTable>
        )}
        <Pager paging={backupPaging} noun="Backups" />
      </SettingsSection>

      <ConfirmModal
        open={restoreTarget !== null}
        title="Restore this backup?"
        description={restoreTarget && (
          <>This replaces <b>all current data</b> with the backup from {dateServer(restoreTarget.createdAt)}.
            A safety copy of the current data is saved first. The app restarts (about 5 seconds).</>
        )}
        confirmLabel="Restore"
        onConfirm={() => (restoreTarget ? restoreListed(restoreTarget) : undefined)}
        onClose={() => setRestoreTarget(null)}
      />
      <ConfirmModal
        open={uploadFile !== null}
        title="Restore from uploaded file?"
        description={uploadFile && (
          <>This replaces <b>all current data</b> with <span className="font-mono">{uploadFile.name}</span> ({formatBytes(uploadFile.size)}).
            The file is checked first; a safety copy of the current data is saved. The app restarts.</>
        )}
        confirmLabel="Upload & restore"
        onConfirm={() => (uploadFile ? restoreUpload(uploadFile) : undefined)}
        onClose={() => setUploadFile(null)}
      />
      <ConfirmModal
        open={deleteTarget !== null}
        title="Delete backup?"
        description={deleteTarget && <><span className="font-mono">{deleteTarget.name}</span> will be removed from disk.</>}
        onConfirm={() => (deleteTarget ? remove(deleteTarget) : undefined)}
        onClose={() => setDeleteTarget(null)}
      />
    </>
  )
}
