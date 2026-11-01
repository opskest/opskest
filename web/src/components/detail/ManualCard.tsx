// Manual monitors: no check runs, so the status is set here (or with
// POST /api/monitors/{id}/status) and stays until it is set again.

import { useState } from 'react'
import { api, errorText } from '../../lib/api'
import type { Status } from '../../lib/types'
import { Button, Input, useToast } from '../ui'

const CHOICES: { status: 'up' | 'degraded' | 'down'; label: string }[] = [
  { status: 'up', label: 'Up' },
  { status: 'degraded', label: 'Degraded' },
  { status: 'down', label: 'Down' },
]

export function ManualCard({ monitorId, current, paused }: { monitorId: number; current: Status; paused: boolean }) {
  const { push } = useToast()
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState('')

  async function set(status: 'up' | 'degraded' | 'down') {
    setBusy(status)
    setError('')
    try {
      await api.post(`/api/monitors/${monitorId}/status`, { status, reason: status === 'up' ? '' : reason.trim() })
      push(`Set to ${status}.`, 'ok')
      if (status === 'up') setReason('')
    } catch (err) {
      setError(errorText(err, 'Could not set the status.'))
    } finally {
      setBusy(null)
    }
  }

  return (
    <section aria-label="Set the status" className="bg-surface border border-default rounded-[14px] p-4 flex flex-col gap-3">
      <div className="flex flex-col gap-0.5">
        <h2 className="text-base font-semibold text-body">Status is set by hand</h2>
        <p className="text-[12px] text-muted">
          Nothing is checked for this monitor. Choose its status; down and degraded send the usual alerts, with the note below as the reason.
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          aria-label="Reason (shown on the monitor and in the alert)"
          placeholder="Reason, e.g. nightly export failed"
          value={reason}
          maxLength={200}
          onChange={e => setReason(e.target.value)}
          className="flex-1 min-w-[220px]"
        />
        {CHOICES.map(c => (
          <Button
            key={c.status}
            size="sm"
            variant={c.status === 'down' ? 'danger' : 'secondary'}
            busy={busy === c.status}
            disabled={paused || (busy !== null && busy !== c.status) || (current === c.status && c.status === 'up')}
            onClick={() => set(c.status)}
          >
            {c.label}
          </Button>
        ))}
      </div>
      {paused && <p className="text-[12px] text-muted">Resume the monitor to change its status.</p>}
      {error && <p role="alert" className="text-[12px] text-danger">{error}</p>}
    </section>
  )
}
