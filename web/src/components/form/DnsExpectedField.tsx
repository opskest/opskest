// The DNS monitor's expected answers: one value per line (e.g. every IP
// behind an ALB), with "Use current answers" to fill them from a live
// lookup — the quick way to set up, or re-baseline after the IPs rotate.

import { useState } from 'react'
import { api, errorText } from '../../lib/api'
import type { DnsMatch } from '../../lib/types'
import { Button, Icon, Textarea, useToast } from '../ui'
import { Field } from './primitives'

interface ResolveOut { record: string; values: string[] }

export function DnsExpectedField({
  target, record, resolver, values, onValues, error,
}: {
  target: string
  record: string
  resolver: string
  values: string[]
  /** match is set when filling several answers, so the monitor alerts on a change. */
  onValues: (values: string[], match?: DnsMatch) => void
  error?: string
}) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)

  async function useCurrent() {
    setBusy(true)
    try {
      const r = await api.post<ResolveOut>('/api/monitors/dns/resolve', { target: target.trim(), record, resolver: resolver.trim() })
      onValues(r.values, r.values.length > 1 ? 'exact' : undefined)
      toast.push(`Using the ${r.values.length} current ${r.record} answer${r.values.length === 1 ? '' : 's'}: ${r.values.join(', ')}. Save to apply.`, 'ok')
    } catch (err) {
      toast.push(errorText(err, 'Lookup failed'), 'danger')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Field
      label="Expected answers"
      optional
      error={error}
      className="sm:col-span-2"
      hint="One per line, e.g. every IP your ALB resolves to. Empty = up whenever the record resolves."
    >
      {id => (
        <div className="flex flex-col gap-2">
          <Textarea
            id={id}
            rows={Math.min(Math.max(values.length, 3), 8)}
            value={values.join('\n')}
            onChange={ev => onValues(ev.target.value.split('\n'))}
            placeholder={record === 'AAAA' ? '2600:1f18::1' : '52.1.2.3\n52.4.5.6'}
            className="font-mono"
            aria-invalid={!!error}
            spellCheck={false}
          />
          <div>
            <Button size="sm" busy={busy} disabled={!target.trim()} onClick={useCurrent} icon={<Icon.Refresh className="w-3.5 h-3.5" />}>
              Use current answers
            </Button>
          </div>
        </div>
      )}
    </Field>
  )
}
