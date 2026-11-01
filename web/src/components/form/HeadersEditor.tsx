// Request headers for http and websocket monitors (spec §6: each can be
// marked secret). A stored secret's value never comes back from the API,
// so it shows "••• set · Replace" and is sent back as `value: null` (keep)
// until someone replaces it. There is deliberately no Reveal.

import { MAX_HEADERS, newHeaderRow, type HeaderRow } from '../../lib/monitorForm'
import type { FieldErrors } from '../../lib/types'
import { Button, Checkbox, Icon, Input } from '../ui'
import { FieldMessage } from './primitives'

export function HeadersEditor({
  rows, onChange, errors, idPrefix,
}: { rows: HeaderRow[]; onChange: (rows: HeaderRow[]) => void; errors: FieldErrors; idPrefix: string }) {
  const editorError = errors.headers ?? errors['settings.headers']

  function update(key: string, patch: Partial<HeaderRow>) {
    onChange(rows.map(r => (r.key === key ? { ...r, ...patch } : r)))
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-[12px] font-medium text-body-2" id={`${idPrefix}-label`}>Headers</span>
        <span className="text-[12px] text-muted">Mark tokens and API keys secret — stored encrypted, never shown again.</span>
      </div>

      {rows.length > 0 && (
        <ul className="flex flex-col gap-2" aria-labelledby={`${idPrefix}-label`}>
          {rows.map((r, i) => {
            const nameErr = errors[`headers.${i}.name`] ?? errors[`headers.${i}`]
            const valueErr = errors[`headers.${i}.value`]
            const kept = r.stored && !r.replacing
            return (
              <li key={r.key} className="flex flex-col gap-1">
                <div className="flex flex-wrap sm:flex-nowrap items-center gap-2">
                  <Input
                    aria-label={`Header ${i + 1} name`}
                    placeholder="Authorization"
                    value={kept ? r.originalName : r.name}
                    disabled={kept}
                    title={kept ? 'Replace the value to rename a secret header' : undefined}
                    onChange={e => update(r.key, { name: e.target.value })}
                    aria-invalid={!!nameErr}
                    className="font-mono w-full sm:w-48 shrink-0"
                    spellCheck={false}
                    autoCapitalize="off"
                  />
                  {kept
                    ? (
                      <div className="flex-1 min-w-0 h-9 flex items-center gap-2 px-3 rounded-lg border border-dashed border-default bg-surface-2/50 text-[13px] text-muted">
                        <span aria-hidden="true" className="tracking-widest">•••</span>
                        <span>set</span>
                        <span aria-hidden="true">·</span>
                        <button type="button" className="font-medium text-accent hover:underline underline-offset-2" onClick={() => update(r.key, { replacing: true, value: '', name: r.originalName })} aria-label={`Replace the value of ${r.originalName}`}>
                          Replace
                        </button>
                      </div>
                    )
                    : (
                      <Input
                        aria-label={`Header ${i + 1} value`}
                        type={r.secret ? 'password' : 'text'}
                        autoComplete={r.secret ? 'new-password' : 'off'}
                        placeholder={r.replacing ? 'New value' : r.secret ? 'Secret value' : 'Value'}
                        value={r.value}
                        onChange={e => update(r.key, { value: e.target.value })}
                        aria-invalid={!!valueErr}
                        className="font-mono flex-1 min-w-0"
                        spellCheck={false}
                      />
                    )}
                  <Checkbox label="Secret" checked={r.secret} disabled={kept} onChange={e => update(r.key, { secret: e.target.checked })} className="shrink-0" />
                  {r.replacing && <Button size="sm" variant="ghost" onClick={() => update(r.key, { replacing: false, value: '', secret: true })}>Cancel</Button>}
                  <Button size="sm" variant="ghost" aria-label={`Remove header ${r.stored ? r.originalName : r.name || i + 1}`} onClick={() => onChange(rows.filter(x => x.key !== r.key))}>
                    <Icon.Trash className="w-3.5 h-3.5" />
                  </Button>
                </div>
                {(nameErr || valueErr) && <FieldMessage error={nameErr ?? valueErr} />}
              </li>
            )
          })}
        </ul>
      )}

      {editorError && <FieldMessage error={editorError} />}
      <div>
        <Button size="sm" onClick={() => onChange([...rows, newHeaderRow()])} disabled={rows.length >= MAX_HEADERS} icon={<Icon.Plus className="w-3.5 h-3.5" />}>
          Add header
        </Button>
      </div>
    </div>
  )
}
