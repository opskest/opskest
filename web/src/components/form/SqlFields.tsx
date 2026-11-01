// Fields of the database monitors (postgres_sql, mysql_sql): the login, and
// either a custom read-only query or a built-in health probe.

import type { SecretDraft, SettingsDraft } from '../../lib/monitorForm'
import type { FieldErrors, MonitorType } from '../../lib/types'
import { Icon, Input, Select, Switch, Textarea } from '../ui'
import { Field, NumberInput, SecretField } from './primitives'

type Probe = SettingsDraft['probe']

const PROBES: { value: Probe; label: string; hint: string }[] = [
  { value: 'query', label: 'Custom query', hint: 'Run your own read-only query and optionally check the first value it returns.' },
  { value: 'long_running', label: 'Slow queries', hint: 'Down while more than the allowed number of queries have been running longer than the limit.' },
  { value: 'deadlocks', label: 'Deadlocks', hint: 'Down when the server reports new deadlocks since the previous check. The first check only records a baseline.' },
  { value: 'blocked', label: 'Lock waits', hint: 'Down while more than the allowed number of sessions are waiting on a lock.' },
  { value: 'connections', label: 'Connection use', hint: 'Down when more than this percent of the connection limit is in use.' },
]

const OPS: { value: SettingsDraft['queryOp']; label: string }[] = [
  { value: '', label: 'Just run it (no comparison)' },
  { value: 'eq', label: 'equals' },
  { value: 'ne', label: 'does not equal' },
  { value: 'gt', label: 'is greater than' },
  { value: 'gte', label: 'is at least' },
  { value: 'lt', label: 'is less than' },
  { value: 'lte', label: 'is at most' },
  { value: 'contains', label: 'contains' },
]

interface Props {
  type: MonitorType
  settings: SettingsDraft
  onSettings: (patch: Partial<SettingsDraft>) => void
  errors: FieldErrors
  basicPass: SecretDraft
  onPassword: (s: SecretDraft) => void
}

export function SqlFields({ type, settings: s, onSettings, errors, basicPass, onPassword }: Props) {
  const e = (k: string) => errors[`settings.${k}`]
  const pg = type === 'postgres_sql'
  const probe = PROBES.find(p => p.value === s.probe) ?? PROBES[0]

  return (
    <div className="flex flex-col gap-5">
      <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
        <Field label="Username" error={e('basicUser')} hint="Use a dedicated read-only account.">
          {id => <Input id={id} value={s.basicUser} onChange={ev => onSettings({ basicUser: ev.target.value })} autoComplete="off" spellCheck={false} aria-invalid={!!e('basicUser')} />}
        </Field>
        <SecretField label="Password" value={basicPass} onChange={onPassword} error={errors['secrets.basicPass']} />
        <Field label="Database" optional error={e('database')} hint={pg ? 'Empty = the account\'s own database.' : 'Empty = no default database.'}>
          {id => <Input id={id} value={s.database} onChange={ev => onSettings({ database: ev.target.value })} autoComplete="off" spellCheck={false} className="font-mono" />}
        </Field>
        <div className="flex flex-col justify-end gap-2 pb-1">
          <Switch label="TLS — encrypt the connection" checked={s.tls} onChange={ev => onSettings({ tls: ev.target.checked })} />
          {s.tls && <Switch label="Ignore TLS errors (self-signed or expired certificates)" checked={s.ignoreTls} onChange={ev => onSettings({ ignoreTls: ev.target.checked })} />}
        </div>
      </div>

      <Field label="What to check" error={e('probe')} hint={probe.hint}>
        {id => (
          <Select
            id={id}
            value={s.probe}
            onChange={ev => {
              const p = ev.target.value as Probe
              onSettings({
                probe: p,
                threshold: p === 'connections' ? (s.threshold >= 1 && s.threshold <= 100 ? s.threshold : 80) : (p === 'query' ? s.threshold : Math.min(s.threshold, 1_000_000)),
                thresholdSecs: s.thresholdSecs > 0 ? s.thresholdSecs : 30,
              })
            }}
          >
            {PROBES.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
          </Select>
        )}
      </Field>

      {s.probe === 'query' && (
        <div className="flex flex-col gap-4">
          <Field label="Query" error={e('query')} hint="One read-only statement (SELECT, WITH, SHOW, EXPLAIN, VALUES or TABLE). It runs in a read-only transaction.">
            {id => (
              <Textarea
                id={id}
                value={s.query}
                onChange={ev => onSettings({ query: ev.target.value })}
                rows={4}
                className="font-mono text-[13px]"
                placeholder="SELECT count(*) FROM orders WHERE created_at > now() - interval '5 minutes'"
                spellCheck={false}
                aria-invalid={!!e('query')}
              />
            )}
          </Field>
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
            <Field label="The first value returned" error={e('queryOp')}>
              {id => (
                <Select id={id} value={s.queryOp} onChange={ev => onSettings({ queryOp: ev.target.value as SettingsDraft['queryOp'] })}>
                  {OPS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                </Select>
              )}
            </Field>
            {s.queryOp !== '' && (
              <Field label="Value" error={e('queryExpected')} hint="Compared as numbers when both sides are numbers.">
                {id => <Input id={id} value={s.queryExpected} onChange={ev => onSettings({ queryExpected: ev.target.value })} className="font-mono" spellCheck={false} aria-invalid={!!e('queryExpected')} />}
              </Field>
            )}
          </div>
          <p className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed">
            <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
            A slow query is a degraded one: set <em>Degraded after</em> under timing and the check goes degraded when the query takes longer. The time shown is the query itself, not the connection.
          </p>
        </div>
      )}

      {s.probe === 'long_running' && (
        <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
          <Field label="Running longer than" error={e('thresholdSecs')}>
            {id => <NumberInput id={id} value={s.thresholdSecs} onValue={n => onSettings({ thresholdSecs: n })} min={1} max={86400} unit="sec" aria-invalid={!!e('thresholdSecs')} />}
          </Field>
          <Field label="Allowed at once" error={e('threshold')} hint="Down above this many. 0 = any slow query.">
            {id => <NumberInput id={id} value={s.threshold} onValue={n => onSettings({ threshold: n })} min={0} max={1000000} aria-invalid={!!e('threshold')} />}
          </Field>
        </div>
      )}

      {(s.probe === 'deadlocks' || s.probe === 'blocked') && (
        <Field label={s.probe === 'deadlocks' ? 'New deadlocks allowed per check' : 'Waiting sessions allowed'} error={e('threshold')} hint="Down above this. 0 = any." className="sm:w-[calc(50%-0.625rem)]">
          {id => <NumberInput id={id} value={s.threshold} onValue={n => onSettings({ threshold: n })} min={0} max={1000000} aria-invalid={!!e('threshold')} />}
        </Field>
      )}

      {s.probe === 'connections' && (
        <Field label="Alert above" error={e('threshold')} hint="Percent of the server's connection limit." className="sm:w-[calc(50%-0.625rem)]">
          {id => <NumberInput id={id} value={s.threshold} onValue={n => onSettings({ threshold: n })} min={1} max={100} unit="%" aria-invalid={!!e('threshold')} />}
        </Field>
      )}

      {s.probe !== 'query' && (
        <p className="flex items-start gap-2.5 rounded-lg border border-default bg-surface-2/60 px-3.5 py-2.5 text-[12px] text-muted leading-relaxed">
          <Icon.Info className="w-4 h-4 mt-0.5 shrink-0" />
          {pg
            ? 'To see other sessions the account needs the pg_monitor role (GRANT pg_monitor TO your_user). Alerts show counts and durations, never query text.'
            : 'To see other sessions the account needs the PROCESS privilege (GRANT PROCESS ON *.* TO your_user). Alerts show counts and durations, never query text.'}
        </p>
      )}
    </div>
  )
}
