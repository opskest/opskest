// The monitor form's Timing & thresholds, Notifications and Runbook sections.

import { channelLabel } from '../../lib/channels'
import { CERT_TYPES, LATENCY_TYPES, type Draft } from '../../lib/monitorForm'
import type { Channel, FieldErrors } from '../../lib/types'
import { Badge, Icon, Input, SkeletonBlock, Switch, Textarea } from '../ui'
import { Field, FieldGrid, FieldMessage, NumberInput, Section, Subsection } from './primitives'
import { DependsOnField, LatencyAlertFields } from './AlertRules'

function every(s: number): string {
  if (!Number.isFinite(s) || s <= 0) return ''
  if (s < 60) return `${s} seconds`
  if (s % 3600 === 0) return s === 3600 ? 'hour' : `${s / 3600} hours`
  if (s % 60 === 0) return s === 60 ? 'minute' : `${s / 60} minutes`
  return `${Math.floor(s / 60)} min ${s % 60} s`
}

export function TimingSection({ draft: d, errors, onPatch }: { draft: Draft; errors: FieldErrors; onPatch: (p: Partial<Draft>) => void }) {
  const maxTimeout = Number.isFinite(d.intervalS) ? d.intervalS * 1000 - 500 : null
  const hasLatency = LATENCY_TYPES.includes(d.type)
  const hasCert = CERT_TYPES.includes(d.type)
  const derived = d.type === 'group' || d.type === 'manual' // nothing runs, so no timeout or retries to tune
  return (
    <Section title="Timing & thresholds" hint="How often it runs, and how sure it must be before calling something down." icon={<Icon.Clock className="w-3.5 h-3.5" />}>
      <FieldGrid>
        <Field label="Check every" error={errors.intervalS} hint={every(d.intervalS) ? `Runs once every ${every(d.intervalS)}. 10–86400 s.` : '10–86400 seconds.'}>
          {fid => <NumberInput id={fid} value={d.intervalS} onValue={n => onPatch({ intervalS: n })} min={10} max={86400} unit="sec" aria-invalid={!!errors.intervalS} />}
        </Field>
        {!derived && <Field label="Timeout" error={errors.timeoutMs} hint={maxTimeout != null && maxTimeout >= 1000 ? `A check slower than this fails. 1000–${maxTimeout} ms.` : 'Must be under the interval.'}>
          {fid => <NumberInput id={fid} value={d.timeoutMs} onValue={n => onPatch({ timeoutMs: n })} min={1000} max={maxTimeout ?? undefined} step={500} unit="ms" aria-invalid={!!errors.timeoutMs} />}
        </Field>}
        {d.type !== 'manual' && <Field
          label="Confirmations before down"
          error={errors.retries}
          hint={Number.isFinite(d.retries) ? (d.retries === 0 ? 'Down on the first failure.' : `Down after ${d.retries + 1} failures in a row.`) : '0–10.'}
        >
          {fid => <NumberInput id={fid} value={d.retries} onValue={n => onPatch({ retries: n })} min={0} max={10} unit="retries" aria-invalid={!!errors.retries} />}
        </Field>}
        {d.type !== 'manual' && (
        <Field label="Retry every" error={errors.retryIntervalS} hint="How soon to re-check while confirming a failure.">
          {fid => <NumberInput id={fid} value={d.retryIntervalS} onValue={n => onPatch({ retryIntervalS: n })} min={10} max={86400} unit="sec" aria-invalid={!!errors.retryIntervalS} />}
        </Field>
        )}
        {hasLatency && (
          <Field label="Degraded above" error={errors.degradedMs} hint="Slower responses mark it degraded. 0 = off.">
            {fid => <NumberInput id={fid} value={d.degradedMs} onValue={n => onPatch({ degradedMs: n })} min={0} max={120000} unit="ms" aria-invalid={!!errors.degradedMs} />}
          </Field>
        )}
        {hasCert && (
          <Field label="Certificate warning" error={errors.sslWarnDays} hint="Degraded when the TLS certificate expires sooner. 0 = off.">
            {fid => <NumberInput id={fid} value={d.sslWarnDays} onValue={n => onPatch({ sslWarnDays: n })} min={0} max={90} unit="days" aria-invalid={!!errors.sslWarnDays} />}
          </Field>
        )}
        {hasCert && (
          <Field label="Certificate expiry alerts" optional error={errors['settings.certThresholds']} hint="Days before expiry at which an alert is sent, once each. Empty = 14, 7, 3, 1.">
            {fid => <Input id={fid} value={d.certThresholds} onChange={e => onPatch({ certThresholds: e.target.value })} placeholder="30, 14, 7, 1" className="font-mono" aria-invalid={!!errors['settings.certThresholds']} spellCheck={false} />}
          </Field>
        )}
      </FieldGrid>

      {d.type !== 'vendor' && !derived && (
        <label className="flex items-start gap-3 rounded-lg border border-default bg-surface-2/50 px-3.5 py-3 cursor-pointer hover:border-strong transition-colors">
          <input
            type="checkbox"
            checked={d.upsideDown}
            onChange={e => onPatch({ upsideDown: e.target.checked })}
            className="mt-0.5 w-3.5 h-3.5 rounded-sm border border-strong accent-accent cursor-pointer shrink-0"
          />
          <span className="flex flex-col gap-0.5">
            <span className="text-[13px] font-medium text-body">Upside-down mode</span>
            <span className="text-[12px] text-muted leading-relaxed">
              Up when the check <strong className="font-semibold text-body-2">fails</strong>, down when it succeeds — for things that must
              stay unreachable, like an admin port that should be closed.
            </span>
          </span>
        </label>
      )}
    </Section>
  )
}

export function AlertingSection({
  draft: d, errors, channels, onPatch, onChannelsTouched, selfId,
}: {
  draft: Draft
  errors: FieldErrors
  channels: Channel[] | null
  onPatch: (p: Partial<Draft>) => void
  onChannelsTouched: () => void
  /** The monitor being edited, so it can't be picked as its own parent. */
  selfId?: number
}) {
  const toggle = (id: number) => (d.channelIds.includes(id) ? d.channelIds.filter(x => x !== id) : [...d.channelIds, id])
  const picked = d.channelIds.length
  return (
    <>
      <Section
        title="Notifications"
        hint="Where alerts go when this monitor goes down, and when it recovers."
        icon={<Icon.Bell className="w-3.5 h-3.5" />}
        aside={channels && channels.length > 0 ? <span className="text-[12px] text-muted tnum">{picked} of {channels.length} selected</span> : undefined}
      >
        <div className="flex flex-col gap-2">
          <span className="sr-only" id="channels-label">Send alerts to</span>
          {channels == null && <SkeletonBlock lines={2} label="Loading channels" />}
          {channels != null && channels.length === 0 && (
            <p className="flex items-start gap-2.5 rounded-lg border border-dashed border-default px-3.5 py-3 text-[12px] text-muted">
              <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
              No channels yet — add one in Settings → Notifications. The monitor is still tracked.
            </p>
          )}
          {channels != null && channels.length > 0 && (
            <div role="group" aria-labelledby="channels-label" className="grid gap-3 sm:grid-cols-2">
              {channels.map(c => {
                const on = d.channelIds.includes(c.id)
                return (
                  <label
                    key={c.id}
                    className={`flex items-center gap-3 rounded-md border px-3 py-2.5 cursor-pointer transition-colors min-w-0
                      ${on ? 'border-accent bg-accent-subtle' : 'border-default bg-surface hover:border-strong'}`}
                  >
                    <input
                      type="checkbox"
                      checked={on}
                      onChange={() => { onChannelsTouched(); onPatch({ channelIds: toggle(c.id) }) }}
                      className="w-3.5 h-3.5 rounded-sm border border-strong accent-accent cursor-pointer shrink-0"
                    />
                    <span className="flex flex-col min-w-0 flex-1">
                      <span className="text-[13px] font-medium text-body truncate">{c.name}</span>
                      <span className="text-[11px] text-muted">{channelLabel(c.type)}</span>
                    </span>
                    {c.isDefault && <Badge tone="accent">Default</Badge>}
                  </label>
                )
              })}
            </div>
          )}
          <FieldMessage error={errors.channelIds} />
        </div>

        <Subsection title="When to notify">
          <FieldGrid>
            <Field label="Repeat the down alert every" error={errors.reminderMin} hint="Until it recovers or is acknowledged. 0 = off.">
              {fid => <NumberInput id={fid} value={d.reminderMin} onValue={n => onPatch({ reminderMin: n })} min={0} max={1440} unit="min" aria-invalid={!!errors.reminderMin} />}
            </Field>
            <div className="sm:pt-7">
              <Switch label="Also notify when it becomes degraded" checked={d.notifyDegraded} onChange={e => onPatch({ notifyDegraded: e.target.checked })} />
            </div>
          </FieldGrid>
        </Subsection>
        {LATENCY_TYPES.includes(d.type) && <LatencyAlertFields draft={d} errors={errors} onPatch={onPatch} />}
        <DependsOnField draft={d} errors={errors} selfId={selfId} onPatch={onPatch} />
      </Section>

      <Section title="Runbook" hint="A link or a short note — shown on alerts and on the wallboard next to this monitor." icon={<Icon.Book className="w-3.5 h-3.5" />}>
        <Field label="Runbook" optional error={errors.runbook}>
          {fid => <Textarea id={fid} rows={3} value={d.runbook} onChange={e => onPatch({ runbook: e.target.value })} placeholder="https://wiki.example.com/runbooks/checkout" aria-invalid={!!errors.runbook} />}
        </Field>
      </Section>
    </>
  )
}
