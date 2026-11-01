// Extra response checks for an HTTP monitor: a list of assertions, all of
// which must pass (status code, response time, a header, the body, or a
// JSON path). Mirrors model.Assertion on the server.

import type { Assertion, FieldErrors } from '../../lib/types'
import { Button, Icon, Input, Select } from '../ui'

const SOURCES: { id: Assertion['source']; label: string }[] = [
  { id: 'status', label: 'Status code' },
  { id: 'latency', label: 'Response time (ms)' },
  { id: 'header', label: 'Header' },
  { id: 'body', label: 'Body' },
  { id: 'json', label: 'JSON path' },
]

const OPS: Record<Assertion['source'], { id: string; label: string }[]> = {
  status: [{ id: 'eq', label: 'equals' }, { id: 'ne', label: 'not equal' }, { id: 'lt', label: '<' }, { id: 'lte', label: '<=' }, { id: 'gt', label: '>' }, { id: 'gte', label: '>=' }],
  latency: [{ id: 'lt', label: '<' }, { id: 'lte', label: '<=' }, { id: 'gt', label: '>' }, { id: 'gte', label: '>=' }],
  header: [{ id: 'eq', label: 'equals' }, { id: 'ne', label: 'not equal' }, { id: 'contains', label: 'contains' }, { id: 'not_contains', label: 'does not contain' }, { id: 'regex', label: 'matches regex' }, { id: 'exists', label: 'exists' }, { id: 'empty', label: 'is empty' }, { id: 'not_empty', label: 'is not empty' }],
  body: [{ id: 'contains', label: 'contains' }, { id: 'not_contains', label: 'does not contain' }, { id: 'regex', label: 'matches regex' }, { id: 'eq', label: 'equals' }, { id: 'empty', label: 'is empty' }, { id: 'not_empty', label: 'is not empty' }],
  json: [{ id: 'eq', label: 'equals' }, { id: 'ne', label: 'not equal' }, { id: 'contains', label: 'contains' }, { id: 'not_contains', label: 'does not contain' }, { id: 'gt', label: '>' }, { id: 'gte', label: '>=' }, { id: 'lt', label: '<' }, { id: 'lte', label: '<=' }, { id: 'regex', label: 'matches regex' }, { id: 'exists', label: 'exists' }, { id: 'empty', label: 'is empty' }, { id: 'not_empty', label: 'is not empty' }],
}

const NO_VALUE = new Set(['exists', 'empty', 'not_empty'])

export function AssertionsEditor({ value, onChange, errors }: { value: Assertion[]; onChange: (v: Assertion[]) => void; errors: FieldErrors }) {
  const set = (i: number, patch: Partial<Assertion>) => onChange(value.map((a, j) => (j === i ? { ...a, ...patch } : a)))
  const err = (i: number) => ['source', 'property', 'op', 'value'].map(k => errors[`settings.assertions.${i}.${k}`]).filter(Boolean).join('; ')

  return (
    <div className="space-y-2">
      {value.length === 0 && <p className="text-[12px] text-muted">No extra checks. The response only has to match the accepted status codes (and the keyword or JSON query, if set).</p>}
      {value.map((a, i) => (
        <div key={i} className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <Select className="w-44" value={a.source} aria-label="What to check"
              onChange={e => { const source = e.target.value as Assertion['source']; set(i, { source, op: OPS[source][0].id, property: source === 'header' || source === 'json' ? a.property : '' }) }}>
              {SOURCES.map(s => <option key={s.id} value={s.id}>{s.label}</option>)}
            </Select>
            {(a.source === 'header' || a.source === 'json') && (
              <Input className="w-48 font-mono" value={a.property ?? ''} placeholder={a.source === 'header' ? 'Content-Type' : 'data.status'}
                aria-label={a.source === 'header' ? 'Header name' : 'JSON path'} onChange={e => set(i, { property: e.target.value })} />
            )}
            <Select className="w-44" value={a.op} aria-label="Comparison" onChange={e => set(i, { op: e.target.value })}>
              {OPS[a.source].map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
            </Select>
            {!NO_VALUE.has(a.op) && (
              <Input className="flex-1 min-w-[8rem] font-mono" value={a.value ?? ''} aria-label="Expected value"
                placeholder={a.source === 'status' ? '200' : a.source === 'latency' ? '500' : 'expected value'} onChange={e => set(i, { value: e.target.value })} />
            )}
            <Button size="sm" variant="ghost" aria-label="Remove check" icon={<Icon.X className="w-3.5 h-3.5" />} onClick={() => onChange(value.filter((_, j) => j !== i))} />
          </div>
          {err(i) && <p className="text-[12px] text-danger">{err(i)}</p>}
        </div>
      ))}
      {value.length < 20 && (
        <Button size="sm" variant="secondary" icon={<Icon.Plus className="w-3.5 h-3.5" />}
          onClick={() => onChange([...value, { source: 'json', property: '', op: 'eq', value: '' }])}>Add check</Button>
      )}
    </div>
  )
}
