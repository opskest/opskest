type RGB = [number, number, number]
function rgb(hex: string): RGB { return [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16)) as RGB }
function luminance(color: RGB): number {
  const linear = color.map(value => { const n = value / 255; return n <= .04045 ? n / 12.92 : ((n + .055) / 1.055) ** 2.4 })
  return linear[0] * .2126 + linear[1] * .7152 + linear[2] * .0722
}
export function contrastRatio(first: string, second: string): number {
  const a = luminance(rgb(first)), b = luminance(rgb(second))
  return (Math.max(a, b) + .05) / (Math.min(a, b) + .05)
}
/** Retain the owner's hue while making links and filled controls readable in either theme. */
export function accessibleBrand(color: string, dark: boolean): { accent: string; foreground: string } | undefined {
  if (!/^#[0-9a-f]{6}$/i.test(color)) return undefined
  const original = rgb(color)
  const surface = dark ? '#18212b' : '#f4f6f8'
  let accent = color.toLowerCase()
  for (let step = 0; step <= 100; step++) {
    const amount = step / 100
    accent = '#' + original.map(value => Math.round(value + ((dark ? 255 : 0) - value) * amount).toString(16).padStart(2, '0')).join('')
    if (contrastRatio(accent, surface) >= 4.5) break
  }
  return { accent, foreground: contrastRatio(accent, '#ffffff') > contrastRatio(accent, '#121826') ? '#ffffff' : '#121826' }
}
