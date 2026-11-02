const PULSE = 'M2.5 21h9l4-8 5 15 4-9 2 2h11'

/** A ring with a heartbeat running through it: monitoring at a glance, one colour, legible at favicon size. */
export default function KestrelMark({ className = '' }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 40" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <defs>
        <mask id="opskest-mark-cut" maskUnits="userSpaceOnUse" x="0" y="0" width="40" height="40">
          <rect width="40" height="40" fill="#fff" stroke="none" />
          <path d={PULSE} stroke="#000" strokeWidth="8.6" />
        </mask>
      </defs>
      <circle cx="20" cy="20" r="13.5" strokeWidth="4.6" mask="url(#opskest-mark-cut)" />
      <path d={PULSE} strokeWidth="3.6" />
    </svg>
  )
}
