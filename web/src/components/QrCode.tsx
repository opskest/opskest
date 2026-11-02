import { useMemo } from 'react'
import { qrModules, qrPath } from '../lib/qr'

/** A QR code drawn as SVG on a white plate, so it scans in light and dark themes. */
export function QrCode({ value, label, size = 176 }: { value: string; label: string; size?: number }) {
  const modules = useMemo(() => { try { return qrModules(value) } catch { return null } }, [value])
  if (!modules) return null
  const side = modules.length + 8
  return (
    <svg role="img" aria-label={label} viewBox={`0 0 ${side} ${side}`} width={size} height={size} shapeRendering="crispEdges"
      className="shrink-0 rounded-[10px] border border-default">
      <rect width={side} height={side} fill="#ffffff" />
      <path d={qrPath(modules)} fill="#000000" />
    </svg>
  )
}
