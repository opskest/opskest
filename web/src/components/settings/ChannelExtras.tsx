// Two optional settings every alert channel has: the wording of its alerts
// (a template with a few {placeholders}) and quiet hours.

import type { ChannelConfigIn, MaskedChannelConfig, QuietHours } from '../../lib/types'
import { TimeInput } from '../DateInputs'
import { Field, Select, Switch, Textarea } from '../ui'

export interface ChannelExtras {
  template: string
  quiet: QuietHours
}

export const PLACEHOLDERS = ['{name}', '{status}', '{reason}', '{duration}', '{url}', '{time}']

const DAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export function defaultExtras(): ChannelExtras {
  return { template: '', quiet: { enabled: false, days: [], start: '22:00', end: '07:00', bypassCritical: true } }
}

export function extrasFromConfig(c: MaskedChannelConfig): ChannelExtras {
  const d = defaultExtras()
  return {
    template: c.template ?? '',
    quiet: c.quiet ? { ...d.quiet, ...c.quiet, days: c.quiet.days ?? [] } : d.quiet,
  }
}

/** What the API gets: an empty template and enabled:false clear what was stored. */
export function extrasToConfig(x: ChannelExtras): Pick<ChannelConfigIn, 'template' | 'quiet'> {
  return {
    template: x.template.trim(),
    quiet: x.quiet.enabled ? { ...x.quiet, days: [...x.quiet.days].sort() } : { ...x.quiet, enabled: false },
  }
}

/** Client-side check mirroring the server's, so the form can say what is wrong before saving. */
export function extrasProblem(x: ChannelExtras): string | null {
  if (x.template.length > 1000) return 'The message template is limited to 1000 characters.'
  const unknown = [...x.template.matchAll(/\{([a-z_]+)\}/g)].map(m => `{${m[1]}}`).find(p => !PLACEHOLDERS.includes(p))
  if (unknown) return `Unknown placeholder ${unknown} in the message template. Use ${PLACEHOLDERS.join(' ')}.`
  if (x.quiet.enabled) {
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(x.quiet.start) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(x.quiet.end)) return 'Quiet hours need a start and end time.'
    if (x.quiet.start === x.quiet.end) return 'Quiet hours must start and end at different times.'
  }
  return null
}

export function ChannelExtrasFields({ value, onChange, zone }: { value: ChannelExtras; onChange: (x: ChannelExtras) => void; zone: string }) {
  const q = value.quiet
  const setQuiet = (p: Partial<QuietHours>) => onChange({ ...value, quiet: { ...q, ...p } })
  const toggleDay = (d: number) => setQuiet({ days: q.days.includes(d) ? q.days.filter(x => x !== d) : [...q.days, d] })
  const crosses = q.start >= q.end

  return (
    <div className="space-y-4 pt-4 border-t border-subtle">
      <Field
        label="Message template (optional)"
        hint={<>Replaces the details of a single alert. Placeholders: <code className="font-mono">{PLACEHOLDERS.join(' ')}</code>. Empty = the standard layout. Plain text; nothing else is evaluated.</>}
      >
        <Textarea
          rows={3}
          value={value.template}
          maxLength={1000}
          onChange={e => onChange({ ...value, template: e.target.value })}
          placeholder="{name} is {status}: {reason} ({time})"
          className="font-mono text-[12px]"
          spellCheck={false}
        />
      </Field>

      <div className="space-y-3">
        <Switch
          label={`Quiet hours — hold this channel's alerts during a recurring window (${zone})`}
          checked={q.enabled}
          onChange={e => setQuiet({ enabled: e.target.checked })}
        />
        {q.enabled && (
          <div className="space-y-3 rounded-lg border border-default bg-surface-2/40 p-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label="From">
                {id => <TimeInput id={id} value={q.start} onChange={v => { if (v) setQuiet({ start: v }) }} buttonLabel="Choose quiet hours start" />}
              </Field>
              <Field label="Until">
                {id => <TimeInput id={id} value={q.end} onChange={v => { if (v) setQuiet({ end: v }) }} buttonLabel="Choose quiet hours end" />}
              </Field>
            </div>
            <div role="group" aria-label="Days the window starts on" className="flex flex-wrap gap-1.5">
              {DAY_LABELS.map((label, d) => {
                const on = q.days.length === 0 || q.days.includes(d)
                return (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={q.days.includes(d)}
                    onClick={() => toggleDay(d)}
                    className={`h-7 px-2.5 rounded-full border text-[12px] font-medium transition-colors ${
                      q.days.includes(d) ? 'border-accent bg-accent-subtle text-body' : on ? 'border-default text-muted hover:text-body' : 'border-default text-faint'
                    }`}
                  >
                    {label}
                  </button>
                )
              })}
            </div>
            <p className="text-[12px] text-muted leading-relaxed">
              {q.days.length === 0 ? 'Every day' : q.days.length === 7 ? 'Every day' : 'Only on the days marked'}
              {crosses ? `, from ${q.start} until ${q.end} the next morning (the window belongs to the day it starts on).` : `, from ${q.start} until ${q.end}.`}
              {' '}Everything else is held back: not queued, not sent later. The Test button still works.
            </p>
            <Field label="Down alerts during the window">
              <Select value={q.bypassCritical ? 'bypass' : 'hold'} onChange={e => setQuiet({ bypassCritical: e.target.value === 'bypass' })}>
                <option value="bypass">Still send: down, reminders and recoveries</option>
                <option value="hold">Hold those too</option>
              </Select>
            </Field>
          </div>
        )}
      </div>
    </div>
  )
}
