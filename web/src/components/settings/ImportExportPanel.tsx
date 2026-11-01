import { useEffect, useRef, useState } from 'react'
import { Link } from 'wouter'
import { api, errorText, unwrapList } from '../../lib/api'
import { Pager, usePaging } from '../Pager'
import { Alert, Button, buttonClass, Field, Select, Input, Textarea, Badge, Tabs, useToast } from '../ui'
import { SettingsCard, SettingsFooter, SettingsSection, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from './SettingsPanel'

// Settings → Import & export. Export downloads JSON/CSV (never secrets).
// Import takes a .json/.csv file or a pasted URL list, shows a dry-run
// preview (create / skip / error per row) and only writes on "Import".

interface RowResult {
  index: number
  name: string
  type: string
  target: string
  action: 'create' | 'skip' | 'error'
  errors?: Record<string, string>
  warnings?: string[]
}
interface ImportResponse { rows: RowResult[]; summary: { create: number; skip: number; error: number } }

type Source =
  | { kind: 'file'; name: string; body: string; contentType: string }
  | { kind: 'urls'; body: string }

const TYPES = ['http', 'tcp', 'ping', 'dns', 'websocket', 'tls', 'smtp', 'ssh', 'redis', 'postgres', 'mysql', 'mongodb', 'mqtt', 'ntp', 'udp', 'amqp', 'memcached', 'banner', 'grpc'] as const

export function ImportExportPanel() {
  const toast = useToast()
  const [mode, setMode] = useState<'file' | 'urls'>('file')
  const [source, setSource] = useState<Source | null>(null)
  const [urlsText, setUrlsText] = useState('')
  const [groupName, setGroupName] = useState('')
  const [newGroupMode, setNewGroupMode] = useState(false)
  const [newGroup, setNewGroup] = useState('')
  const [urlType, setUrlType] = useState<(typeof TYPES)[number]>('http')
  const [intervalS, setIntervalS] = useState(60)
  const [groups, setGroups] = useState<{ id: number; name: string }[]>([])
  const [preview, setPreview] = useState<ImportResponse | null>(null)
  const [busy, setBusy] = useState<'preview' | 'import' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<ImportResponse['summary'] | null>(null)
  const previewPaging = usePaging(preview?.rows ?? [], { resetKey: preview })
  const fileInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    api.get<unknown>('/api/groups').then(d => setGroups(unwrapList(d, 'groups'))).catch(() => {})
  }, [])

  function reset() {
    setPreview(null); setError(null); setDone(null)
  }

  async function readFile(f: File) {
    reset()
    const text = await f.text()
    const isCSV = f.name.toLowerCase().endsWith('.csv') || f.type.includes('csv')
    setSource({ kind: 'file', name: f.name, body: text, contentType: isCSV ? 'text/csv' : 'application/json' })
  }

  function currentRequest(): { body: string; contentType: string } | null {
    if (mode === 'file') {
      if (!source || source.kind !== 'file') return null
      return { body: source.body, contentType: source.contentType }
    }
    const urls = urlsText.split(/\r?\n/).map(s => s.trim()).filter(Boolean)
    if (urls.length === 0) return null
    return {
      body: JSON.stringify({ urls, groupName: groupName || undefined, type: urlType, intervalS }),
      contentType: 'application/json',
    }
  }

  async function run(dryRun: boolean) {
    const req = currentRequest()
    if (!req) {
      setError(mode === 'file' ? 'Choose a .json or .csv file first.' : 'Paste at least one URL or host.')
      return
    }
    setBusy(dryRun ? 'preview' : 'import'); setError(null)
    try {
      const res = await api.send<ImportResponse>('POST', `/api/import/monitors?dryRun=${dryRun}`, req.body, req.contentType)
      if (dryRun) {
        setPreview(res)
      } else {
        setDone(res.summary); setPreview(null)
        toast.push(`Imported ${res.summary.create} monitor${res.summary.create === 1 ? '' : 's'}`, 'ok')
      }
    } catch (e) {
      setError(errorText(e, dryRun ? 'Preview failed' : 'Import failed'))
    } finally {
      setBusy(null)
    }
  }

  const exportLink = `${buttonClass('secondary', 'md')} w-full justify-center`

  return (
    <>
      <SettingsSection title="Export monitors" description="All monitors with groups, tags, channels and settings. Secrets (passwords, tokens, secret headers) are never exported.">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <SettingsCard title="JSON" action={<a className={exportLink} href="/api/export/monitors?format=json" download="opskest-monitors.json">Export JSON</a>}>
            The complete picture, ready to import into another Opskest.
          </SettingsCard>
          <SettingsCard title="CSV" action={<a className={exportLink} href="/api/export/monitors?format=csv" download="opskest-monitors.csv">Export CSV</a>}>
            One row per monitor, for spreadsheets and bulk edits.
          </SettingsCard>
          <SettingsCard title="CSV template" action={<a className={exportLink} href="/api/export/template.csv" download="opskest-template.csv">Download CSV template</a>}>
            An empty file in the import layout, to fill in and bring back.
          </SettingsCard>
        </div>
      </SettingsSection>

      <SettingsSection title="Import monitors" description="Preview first. Nothing is created until you click Import. Missing groups and tags are created; duplicates (same name, type and target) are skipped, so importing the same file twice is safe.">
        <Tabs
          label="Import source"
          active={mode}
          onChange={v => { setMode(v); reset() }}
          tabs={[{ key: 'file' as const, label: 'From file (JSON / CSV)' }, { key: 'urls' as const, label: 'Paste URLs' }]}
        />

        <div className="grid grid-cols-1 items-stretch gap-5 lg:grid-cols-2">
          {mode === 'file' ? (
            <>
              <div className="flex min-h-[15rem] flex-col items-center justify-center gap-3 rounded-[14px] border border-dashed border-strong bg-surface-2/40 p-6 text-center"
                onDragOver={e => e.preventDefault()}
                onDrop={e => { e.preventDefault(); const f = e.dataTransfer.files?.[0]; if (f) void readFile(f) }}>
                <input ref={fileInput} type="file" accept=".json,.csv,application/json,text/csv" className="hidden"
                  onChange={e => { const f = e.target.files?.[0]; if (f) void readFile(f); e.target.value = '' }} />
                <p className="text-sm text-body">{source?.kind === 'file' ? <span className="font-mono">{source.name}</span> : 'Drop a file here'}</p>
                <Button variant="secondary" onClick={() => fileInput.current?.click()}>Choose file…</Button>
              </div>
              <div className="flex min-h-[15rem] flex-col justify-center gap-2 rounded-[14px] border border-default bg-surface-2/40 p-5">
                <h4 className="text-sm font-semibold text-body">What the file can hold</h4>
                <ul className="list-disc space-y-1.5 pl-5 text-[13px] leading-relaxed text-muted">
                  <li>A JSON file from <em>Export JSON</em>, or a CSV in the layout of the template above.</li>
                  <li>Groups and tags that do not exist yet are created for you.</li>
                  <li>Rows that match an existing monitor (same name, type and target) are skipped.</li>
                  <li>Passwords and tokens are never part of a file; add them after importing.</li>
                </ul>
              </div>
            </>
          ) : (
            <>
              <Field label="URLs or hosts" hint="One per line, e.g. https://api.example.com/health or db.internal:3306" className="h-full [&>textarea]:min-h-[11rem] [&>textarea]:flex-1">
                <Textarea rows={8} value={urlsText} onChange={e => { setUrlsText(e.target.value); reset() }} placeholder={'https://example.com\nhttps://api.example.com/health'} className="font-mono text-xs" />
              </Field>
              <div className="flex min-w-0 flex-col gap-4 rounded-[14px] border border-default bg-surface-2/40 p-5">
                <Field label="Type">
                  <Select value={urlType} onChange={e => setUrlType(e.target.value as (typeof TYPES)[number])}>
                    {TYPES.map(t => <option key={t} value={t}>{t.toUpperCase()}</option>)}
                  </Select>
                </Field>
                <Field label="Group" hint={newGroupMode ? 'This group is created when you import.' : 'Put the imported monitors in a group, or create a new one.'}>
                  <div className="flex flex-col gap-2">
                    <Select
                      value={newGroupMode ? '__new__' : groupName}
                      onChange={e => {
                        const v = e.target.value
                        if (v === '__new__') { setNewGroupMode(true); setGroupName(newGroup) } else { setNewGroupMode(false); setGroupName(v) }
                      }}
                    >
                      <option value="">Ungrouped</option>
                      {groups.map(g => <option key={g.id} value={g.name}>{g.name}</option>)}
                      <option value="__new__">New group…</option>
                    </Select>
                    {newGroupMode && <Input value={newGroup} onChange={e => { setNewGroup(e.target.value); setGroupName(e.target.value) }} placeholder="Name of the new group" maxLength={120} autoFocus />}
                  </div>
                </Field>
                <Field label="Interval (seconds)">
                  <Input type="number" min={10} max={86400} value={intervalS} onChange={e => setIntervalS(Number(e.target.value) || 60)} />
                </Field>
              </div>
            </>
          )}
        </div>

        <SettingsFooter status={error ? <span className="text-danger">{error}</span> : undefined}>
          <Button variant="secondary" busy={busy === 'preview'} onClick={() => run(true)}>Preview</Button>
          <Button variant="primary" busy={busy === 'import'} disabled={!preview || preview.summary.create === 0}
            onClick={() => run(false)}>
            {preview ? `Import ${preview.summary.create} monitor${preview.summary.create === 1 ? '' : 's'}` : 'Import'}
          </Button>
        </SettingsFooter>

        {done && (
          <Alert tone="ok">
            <span className="flex flex-wrap items-center justify-between gap-2">
              <span>Created {done.create}, skipped {done.skip}, errors {done.error}.</span>
              <Link href="/monitors" className="font-medium underline-offset-2 hover:underline">Go to monitors →</Link>
            </span>
          </Alert>
        )}

        {preview && (
          <div className="space-y-2">
            <div className="flex flex-wrap gap-2">
              <Badge tone="ok">{preview.summary.create} to create</Badge>
              <Badge tone="neutral">{preview.summary.skip} skipped</Badge>
              <Badge tone={preview.summary.error ? 'danger' : 'neutral'}>{preview.summary.error} errors</Badge>
            </div>
            <SettingsTable className="max-h-96 overflow-y-auto">
              <table className={`${ST_TABLE} min-w-[640px]`}>
                <thead className={ST_HEAD}>
                  <tr><th className={ST_TH}>#</th><th className={ST_TH}>Name</th><th className={ST_TH}>Type</th><th className={ST_TH}>Target</th><th className={ST_TH}>Result</th><th className={ST_TH}>Details</th></tr>
                </thead>
                <tbody className={ST_BODY}>
                  {previewPaging.rows.map(r => (
                    <tr key={r.index} className={ST_ROW}>
                      <td className={`${ST_TD} text-faint font-mono`}>{r.index + 1}</td>
                      <td className={`${ST_TD} text-body`}>{r.name || '—'}</td>
                      <td className={`${ST_TD} text-muted uppercase`}>{r.type}</td>
                      <td className={`${ST_TD} text-muted font-mono truncate max-w-[18rem]`} title={r.target}>{r.target}</td>
                      <td className={ST_TD}><Badge tone={r.action === 'create' ? 'ok' : r.action === 'skip' ? 'neutral' : 'danger'}>{r.action}</Badge></td>
                      <td className={`${ST_TD} text-muted`}>
                        {r.errors && Object.entries(r.errors).map(([k, v]) => <div key={k} className="text-danger">{k}: {v}</div>)}
                        {r.warnings?.map((w, i) => <div key={i}>{w}</div>)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </SettingsTable>
            <Pager paging={previewPaging} noun="Rows" />
          </div>
        )}
      </SettingsSection>
    </>
  )
}
