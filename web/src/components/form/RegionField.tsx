import { COUNTRY_GROUPS, MAX_COUNTRIES, hostedCountry } from '../../lib/countries'
import { serverRegion, serverTimeZone } from '../../lib/runtime'
import { Tooltip, useToast } from '../ui'

// Monitoring region for one monitor: Worldwide (the countries set in
// Settings → Multi-region, checked on rotation) plus this server,
// this server only, or a custom list of countries.

export function RegionField({ mode, countries, onChange, error }: {
  mode: '' | 'india' | 'local' | 'custom'
  countries: string[]
  onChange: (mode: '' | 'india' | 'local' | 'custom', countries: string[]) => void
  error?: string
}) {
  const location = serverRegion()
  const toast = useToast()
  const hosted = hostedCountry(location, serverTimeZone())
  const selected = mode === 'india' ? 'local' : mode
  const options: { id: '' | 'local' | 'custom'; label: string; desc: string }[] = [
    { id: '', label: 'Worldwide', desc: `${location} every check, plus the worldwide countries from Workspace settings on rotation.` },
    { id: 'local', label: 'This server only', desc: `Checks run from ${location === 'This server' ? 'this server' : location}. No checks from other countries.` },
    { id: 'custom', label: 'Custom', desc: `${location} every check, plus the countries you pick below.` },
  ]
  const toggle = (c: string) => onChange('custom', countries.includes(c) ? countries.filter(x => x !== c) : countries.length >= MAX_COUNTRIES ? countries : [...countries, c])

  return (
    <div className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-3" role="radiogroup" aria-label="Monitoring region">
        {options.map(o => (
          <button key={o.id || 'ww'} type="button" role="radio" aria-checked={selected === o.id} onClick={() => onChange(o.id, o.id === 'custom' ? countries : [])}
            className={`text-left rounded-lg border px-3 py-2.5 transition ${selected === o.id ? 'border-accent bg-accent/[0.06] ring-1 ring-accent' : 'border-default bg-surface hover:border-strong'}`}>
            <span className="block text-[13px] font-semibold text-body">{o.label}</span>
            <span className="block text-[11px] text-muted mt-0.5 leading-snug">{o.desc}</span>
          </button>
        ))}
      </div>
      {mode === 'custom' && (
        <div className="flex flex-col gap-2">
          <span className="text-[12px] text-muted">{countries.filter(c => c !== hosted).length} of up to {MAX_COUNTRIES} selected</span>
          {COUNTRY_GROUPS.map(g => (
            <div key={g.region}>
              <div className="text-[11px] font-semibold uppercase tracking-wider text-faint mb-1">{g.region}</div>
              <div className="flex flex-wrap gap-1.5">
                {g.list.map(([code, name]) => {
                  if (code === hosted) {
                    const why = `Hosted country: every check already runs from ${name}.`
                    return (
                      <Tooltip key={code} content={why}>
                        <button type="button" aria-disabled="true" aria-label={`${name}: ${why}`} onClick={() => toast.push(why, 'neutral')}
                          className="inline-flex h-8 cursor-not-allowed items-center gap-2 rounded-full border border-default px-3 text-[12px] text-muted opacity-40">
                          <span className="font-mono text-[11px] font-semibold">{code}</span>{name}
                        </button>
                      </Tooltip>
                    )
                  }
                  const on = countries.includes(code)
                  return (
                    <button key={code} type="button" onClick={() => toggle(code)} aria-pressed={on} disabled={!on && countries.length >= MAX_COUNTRIES}
                      className={`inline-flex h-8 items-center gap-2 rounded-full border px-3 text-[12px] transition-colors disabled:opacity-40 ${on ? 'border-accent bg-accent-subtle text-body' : 'border-default text-muted hover:text-body hover:border-strong'}`}>
                      <span className="font-mono text-[11px] font-semibold">{code}</span>{name}
                    </button>
                  )
                })}
              </div>
            </div>
          ))}
        </div>
      )}
      {error && <p className="text-[12px] text-danger">{error}</p>}
    </div>
  )
}
