// A DNS monitor's expected answers, with a one-click "Rotate to current
// IPs": resolve now, show what changed (kept / removed / new), and on
// confirm save the current answers as the new expected set. Built for
// ALBs, whose IPs AWS rotates without notice.

import { useCallback, useEffect, useState } from 'react'
import { api, errorText } from '../../lib/api'
import type { MonitorOut } from '../../lib/types'
import { Button, Icon, Modal, useToast } from '../ui'

interface ResolveOut { record: string; values: string[]; expected: string[] }
interface RotateOut { previous: string[]; current: string[]; match: string }

const MATCH_LABEL: Record<string, string> = {
  exact: 'exactly these',
  all: 'all of these present',
  equals: 'any of these',
  contains: 'any containing these',
}

export function DnsRotateCard({ monitorId, isAdmin, down, reason }: {
  monitorId: number
  isAdmin: boolean
  down: boolean
  reason?: string
}) {
  const toast = useToast()
  const [expected, setExpected] = useState<string[] | null>(null)
  const [record, setRecord] = useState('A')
  const [match, setMatch] = useState('')
  const [preview, setPreview] = useState<ResolveOut | null>(null)
  const [busy, setBusy] = useState<'' | 'resolve' | 'save'>('')

  const load = useCallback(() => {
    api.get<MonitorOut>(`/api/monitors/${monitorId}`).then(m => {
      const s = m.settings ?? {}
      setExpected(s.expectedValues?.length ? s.expectedValues : s.expected ? [s.expected] : [])
      setRecord(s.record || 'A')
      setMatch(s.match || '')
    }).catch(() => setExpected(null))
  }, [monitorId])
  useEffect(() => { setExpected(null); setPreview(null); load() }, [load])

  async function openPreview() {
    setBusy('resolve')
    try {
      setPreview(await api.post<ResolveOut>('/api/monitors/dns/resolve', { id: monitorId }))
    } catch (err) {
      toast.push(errorText(err, 'Lookup failed'), 'danger')
    } finally {
      setBusy('')
    }
  }

  async function confirm() {
    if (!preview) return
    setBusy('save')
    try {
      const r = await api.post<RotateOut>(`/api/monitors/${monitorId}/dns/rotate`, { values: preview.values })
      setPreview(null)
      setExpected(r.current)
      setMatch(r.match)
      toast.push(`Expected IPs updated — now using ${r.current.length} ${r.current.length === 1 ? 'IP' : 'IPs'}: ${r.current.join(', ')}. Re-checking now.`, 'ok')
    } catch (err) {
      toast.push(errorText(err, 'Update failed'), 'danger')
    } finally {
      setBusy('')
    }
  }

  if (expected === null) return null

  // The checker's "changed: missing …; new …" reason means the IPs rotated.
  const drifted = down && !!reason && reason.includes(' changed: ')
  const kept = preview ? preview.values.filter(v => preview.expected.includes(v)) : []
  const added = preview ? preview.values.filter(v => !preview.expected.includes(v)) : []
  const removed = preview ? preview.expected.filter(v => !preview.values.includes(v)) : []
  const unchanged = preview !== null && added.length === 0 && removed.length === 0

  return (
    <div className={`p-4 rounded-[14px] border space-y-3 ${drifted ? 'bg-warn/10 border-warn/40' : 'bg-surface border-default'}`}>
      <div className="flex items-start justify-between gap-3 flex-wrap">
        <div>
          <h3 className="text-base font-semibold text-body">Expected {record} answers</h3>
          <p className="text-sm text-muted mt-2">
            {expected.length === 0 ? 'None set — up whenever the record resolves.' : `Up when the answers are ${MATCH_LABEL[match] ?? MATCH_LABEL.equals}.`}
          </p>
        </div>
        {isAdmin && (
          <Button size="sm" variant={drifted ? 'primary' : 'secondary'} busy={busy === 'resolve'} onClick={openPreview} icon={<Icon.Refresh className="w-3.5 h-3.5" />}>
            Rotate to current IPs
          </Button>
        )}
      </div>
      {drifted && <p className="text-[12px] text-warn">The answers changed. If this is an expected rotation, accept the current IPs.</p>}
      {expected.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {expected.map(v => <span key={v} className="font-mono text-[12px] px-2 py-0.5 rounded-md bg-surface-2 border border-default/60">{v}</span>)}
        </div>
      )}

      <Modal
        open={preview !== null}
        title="Use the current answers?"
        description={unchanged ? 'The record already resolves to exactly the expected values.' : 'The monitor will expect these answers from now on and re-check immediately.'}
        onClose={() => setPreview(null)}
        footer={(
          <>
            <Button onClick={() => setPreview(null)}>Cancel</Button>
            <Button variant="primary" busy={busy === 'save'} onClick={confirm} icon={<Icon.Check className="w-3.5 h-3.5" />}>
              Use {preview?.values.length ?? 0} current {preview?.values.length === 1 ? 'answer' : 'answers'}
            </Button>
          </>
        )}
      >
        <ul className="flex flex-col gap-1 font-mono text-[12px]">
          {kept.map(v => <li key={`k${v}`} className="text-body"><span className="inline-block w-16 text-muted font-sans text-[11px]">kept</span>{v}</li>)}
          {added.map(v => <li key={`a${v}`} className="text-ok"><span className="inline-block w-16 font-sans text-[11px]">new</span>{v}</li>)}
          {removed.map(v => <li key={`r${v}`} className="text-danger line-through"><span className="inline-block w-16 font-sans text-[11px] no-underline">removed</span>{v}</li>)}
        </ul>
      </Modal>
    </div>
  )
}
