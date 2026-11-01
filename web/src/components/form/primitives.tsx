// Form building blocks shared by the monitor form's sections: a titled
// section card, a labelled field, a sub-heading, a collapsible block, a
// number input over a `number` value (empty = NaN, which validation
// reports) with an optional unit, and the write-only secret field
// ("••• set · Replace · Clear").

import { useId, useState, type InputHTMLAttributes, type ReactNode } from 'react'
import type { SecretDraft } from '../../lib/monitorForm'
import { Button, Icon, Input, Textarea } from '../ui'

export function Section({
  title, hint, children, id, icon, aside,
}: { title: string; hint?: ReactNode; children: ReactNode; id?: string; icon?: ReactNode; aside?: ReactNode }) {
  const hid = useId()
  return (
    <section id={id} aria-labelledby={hid} className="min-w-0 scroll-mt-24 rounded-[14px] border border-default bg-surface px-5 pb-5 sm:px-6">
      <header className="flex items-start gap-3 pb-4 pt-5 border-b border-default">
        {icon && (
          <span aria-hidden="true" className="mt-px w-7 h-7 rounded-md bg-surface-2 border border-default text-muted flex items-center justify-center shrink-0">
            {icon}
          </span>
        )}
        <div className="flex flex-col gap-0.5 min-w-0 flex-1">
          <h2 id={hid} className="text-base font-semibold text-body leading-7">{title}</h2>
          {hint && <p className="text-[12px] text-muted leading-relaxed">{hint}</p>}
        </div>
        {aside && <div className="shrink-0 self-center">{aside}</div>}
      </header>
      <div className="pt-5 flex flex-col gap-5">{children}</div>
    </section>
  )
}

/** Two equal columns (one under 640px) with equal gutters; paired fields share a row. */
export function FieldGrid({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`grid grid-cols-1 gap-x-5 gap-y-5 sm:grid-cols-2 items-start ${className}`}>{children}</div>
}

/** A labelled control: label on top, then the control, then an error or a hint. */
export function Field({
  label, hint, error, children, className = '', optional,
}: {
  label: string; hint?: ReactNode; error?: string; className?: string; optional?: boolean
  children: ReactNode | ((id: string) => ReactNode)
}) {
  const id = useId()
  return (
    <div className={`flex flex-col gap-1.5 min-w-0 ${className}`}>
      <label htmlFor={id} className="text-[12px] font-medium text-body-2 flex items-baseline gap-1.5">
        {label}
        {optional && <span className="text-[11px] font-normal text-faint">Optional</span>}
      </label>
      {typeof children === 'function' ? children(id) : children}
      <FieldMessage error={error} hint={hint} />
    </div>
  )
}

export function FieldMessage({ error, hint }: { error?: string; hint?: ReactNode }) {
  if (error) {
    return (
      <p className="flex items-start gap-1.5 text-[12px] text-danger leading-snug">
        <Icon.Alert className="w-3.5 h-3.5 mt-px shrink-0" />
        <span className="min-w-0 break-words">{error}</span>
      </p>
    )
  }
  return hint ? <p className="text-[12px] text-muted leading-snug">{hint}</p> : null
}

/** A small heading inside a section, separating one group of fields from the next. */
export function Subsection({ title, hint, children, aside }: { title: string; hint?: ReactNode; children: ReactNode; aside?: ReactNode }) {
  const hid = useId()
  return (
    <div role="group" aria-labelledby={hid} className="flex flex-col gap-4 min-w-0 pt-5 border-t border-default first:pt-0 first:border-t-0">
      <div className="flex items-start gap-3">
        <div className="flex flex-col gap-0.5 min-w-0 flex-1">
          <h3 id={hid} className="text-sm font-semibold text-body">{title}</h3>
          {hint && <p className="text-[12px] text-muted leading-relaxed">{hint}</p>}
        </div>
        {aside}
      </div>
      {children}
    </div>
  )
}

/** A collapsible block; `summary` says what is inside while it is closed. */
export function Disclosure({
  title, summary, defaultOpen = false, forceOpen = false, children,
}: { title: string; summary?: ReactNode; defaultOpen?: boolean; forceOpen?: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(defaultOpen)
  const id = useId()
  const shown = open || forceOpen
  return (
    <div className="rounded-md border border-default bg-surface-2/40 min-w-0">
      <button
        type="button"
        aria-expanded={shown}
        aria-controls={id}
        onClick={() => setOpen(!shown)}
        className="w-full flex items-center gap-2.5 px-4 py-3 text-left rounded-md hover:bg-surface-hover/60 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
      >
        <Icon.Chevron className={`w-3.5 h-3.5 text-muted shrink-0 transition-transform ${shown ? 'rotate-90' : ''}`} />
        <span className="text-[13px] font-semibold text-body">{title}</span>
        {summary && !shown && <span className="text-[12px] text-muted truncate min-w-0">{summary}</span>}
      </button>
      {shown && <div id={id} className="px-4 pb-4 pt-1 flex flex-col gap-5">{children}</div>}
    </div>
  )
}

export function NumberInput({
  value, onValue, className = '', unit, ...rest
}: { value: number; onValue: (n: number) => void; unit?: string } & Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange' | 'type'>) {
  const input = (
    <Input
      {...rest}
      type="number"
      inputMode="numeric"
      value={Number.isFinite(value) ? String(value) : ''}
      onChange={e => onValue(e.target.value === '' ? Number.NaN : Number(e.target.value))}
      className={`tnum w-full ${unit ? 'pr-14' : ''} ${className}`}
    />
  )
  if (!unit) return input
  return (
    <div className="relative min-w-0">
      {input}
      <span aria-hidden="true" className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[12px] text-faint">{unit}</span>
    </div>
  )
}

export function SecretField({
  label, value, onChange, error, hint, multiline, placeholder,
}: { label: string; value: SecretDraft; onChange: (s: SecretDraft) => void; error?: string; hint?: string; multiline?: boolean; placeholder?: string }) {
  return (
    <Field label={label} error={error} hint={hint}>
      {id => {
        if (value.isSet && value.mode === 'keep') {
          return (
            <div id={id} role="group" aria-label={`${label}: a value is set`} className="h-9 flex items-center gap-2 px-3 rounded-lg border border-dashed border-default bg-surface-2/50 text-[13px] text-muted">
              <Icon.Lock className="w-3.5 h-3.5 text-faint" />
              <span aria-hidden="true" className="tracking-widest">•••</span>
              <span>set</span>
              <span className="flex-1" />
              <button type="button" className="font-medium text-accent hover:underline underline-offset-2" onClick={() => onChange({ ...value, mode: 'replace', value: '' })}>
                Replace
              </button>
              <span aria-hidden="true" className="text-faint">·</span>
              <button type="button" className="font-medium text-muted hover:text-danger" onClick={() => onChange({ ...value, mode: 'clear', value: '' })}>
                Clear
              </button>
            </div>
          )
        }
        if (value.isSet && value.mode === 'clear') {
          return (
            <div id={id} role="group" aria-label={`${label}: will be removed`} className="h-9 flex items-center gap-2 px-3 rounded-lg border border-dashed border-danger text-[13px] text-danger">
              Will be removed on save
              <span className="flex-1" />
              <button type="button" className="font-medium underline underline-offset-2" onClick={() => onChange({ ...value, mode: 'keep' })}>Undo</button>
            </div>
          )
        }
        if (multiline) {
          // A PEM block: shown while typing so it can be checked, never shown again once saved.
          return (
            <div className="flex items-start gap-2">
              <Textarea
                id={id}
                rows={5}
                autoComplete="off"
                placeholder={value.isSet ? 'New value' : placeholder}
                value={value.value}
                onChange={e => onChange({ ...value, value: e.target.value })}
                aria-invalid={!!error}
                className="flex-1 min-w-0 font-mono text-[12px]"
                spellCheck={false}
              />
              {value.isSet && <Button size="sm" variant="ghost" onClick={() => onChange({ ...value, mode: 'keep', value: '' })}>Cancel</Button>}
            </div>
          )
        }
        return (
          <div className="flex items-center gap-2">
            <Input
              id={id}
              type="password"
              autoComplete="new-password"
              placeholder={value.isSet ? 'New value' : ''}
              value={value.value}
              onChange={e => onChange({ ...value, value: e.target.value })}
              aria-invalid={!!error}
              className="flex-1 min-w-0 font-mono"
              spellCheck={false}
            />
            {value.isSet && <Button size="sm" variant="ghost" onClick={() => onChange({ ...value, mode: 'keep', value: '' })}>Cancel</Button>}
          </div>
        )
      }}
    </Field>
  )
}
