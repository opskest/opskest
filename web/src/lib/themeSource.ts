/**
 * The element whose colour variables a portalled overlay (dialog, drawer,
 * tooltip) should copy. A status page header remaps colours for its own
 * contents, so a trigger inside it would otherwise paint the overlay in the
 * header's brand tones; the overlay belongs to the page, not the header.
 */
export function themeSource(el: Element): Element {
  const header = el.closest('.status-header-solid, .status-header-tinted')
  return header?.parentElement ?? el
}
