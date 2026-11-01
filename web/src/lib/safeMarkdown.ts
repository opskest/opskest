// A small markdown subset for status page copy (description, footer, pinned
// announcement). It parses into a plain tree that the React component turns
// into elements: there is no HTML in, none out, and no innerHTML anywhere.
//
// Supported: paragraphs, "- " and "1. " lists, "## " and "### " headings,
// "> " quotes, **bold**, *italic* / _italic_, `code`, [text](url),
// ![alt](https://image), bare http(s) addresses and \ escapes.
// Links are limited to http, https and mailto; images to https. Anything
// else (javascript:, data:, relative paths, raw tags) stays visible as text.

export type Inline =
  | { t: 'text'; v: string }
  | { t: 'strong'; c: Inline[] }
  | { t: 'em'; c: Inline[] }
  | { t: 'code'; v: string }
  | { t: 'link'; href: string; c: Inline[] }
  | { t: 'image'; src: string; alt: string }
  | { t: 'br' }

export type Block =
  | { t: 'p'; c: Inline[] }
  | { t: 'h'; level: 2 | 3; c: Inline[] }
  | { t: 'ul'; items: Inline[][] }
  | { t: 'ol'; items: Inline[][] }
  | { t: 'quote'; c: Inline[] }

const CONTROL = /[\u0000-\u0008\u000b-\u001f\u007f]/
const MAX_DEPTH = 4

/** The address for a link, or null when it is not http, https or mailto. */
export function safeHref(raw: string): string | null {
  const url = raw.trim()
  if (!url || url.length > 2000 || CONTROL.test(url) || /\s/.test(url)) return null
  if (/^mailto:[^\s@]+@[^\s@]+$/i.test(url)) return url
  if (!/^https?:\/\//i.test(url)) return null
  try {
    const u = new URL(url)
    if ((u.protocol !== 'https:' && u.protocol !== 'http:') || !u.hostname) return null
    return u.href
  } catch {
    return null
  }
}

/** The address for an image: https only. */
export function safeImage(raw: string): string | null {
  const href = safeHref(raw)
  return href && href.startsWith('https://') ? href : null
}

const ESCAPABLE = '\\`*_[]()!#>-'

function closing(src: string, from: number, token: string): number {
  let i = from
  while (i < src.length) {
    if (src[i] === '\\') { i += 2; continue }
    if (src.startsWith(token, i)) return i
    i++
  }
  return -1
}

function pushText(out: Inline[], v: string) {
  if (!v) return
  const last = out[out.length - 1]
  if (last && last.t === 'text') last.v += v
  else out.push({ t: 'text', v })
}

/** Bracket-balanced "(url)" after position i, or null. */
function parenthesised(src: string, i: number): { url: string; end: number } | null {
  if (src[i] !== '(') return null
  const close = src.indexOf(')', i + 1)
  if (close < 0) return null
  return { url: src.slice(i + 1, close), end: close + 1 }
}

export function parseInline(src: string, depth = 0): Inline[] {
  const out: Inline[] = []
  let i = 0
  while (i < src.length) {
    const ch = src[i]
    if (ch === '\\' && i + 1 < src.length && ESCAPABLE.includes(src[i + 1])) { pushText(out, src[i + 1]); i += 2; continue }
    if (ch === '\n') { out.push({ t: 'br' }); i++; continue }
    if (ch === '`') {
      const end = src.indexOf('`', i + 1)
      if (end > i + 1) { out.push({ t: 'code', v: src.slice(i + 1, end) }); i = end + 1; continue }
    }
    if (ch === '!' && src[i + 1] === '[') {
      const close = src.indexOf(']', i + 2)
      const dest = close > 0 ? parenthesised(src, close + 1) : null
      if (close > 0 && dest) {
        const image = safeImage(dest.url)
        if (image) { out.push({ t: 'image', src: image, alt: src.slice(i + 2, close) }); i = dest.end; continue }
      }
    }
    if (ch === '[' && depth < MAX_DEPTH) {
      const close = src.indexOf(']', i + 1)
      const dest = close > 0 ? parenthesised(src, close + 1) : null
      if (close > 0 && dest) {
        const href = safeHref(dest.url)
        if (href) { out.push({ t: 'link', href, c: parseInline(src.slice(i + 1, close), depth + 1) }); i = dest.end; continue }
      }
    }
    if ((src.startsWith('**', i) || src.startsWith('__', i)) && depth < MAX_DEPTH) {
      const token = src.slice(i, i + 2)
      const end = closing(src, i + 2, token)
      if (end > i + 2) { out.push({ t: 'strong', c: parseInline(src.slice(i + 2, end), depth + 1) }); i = end + 2; continue }
    }
    if ((ch === '*' || ch === '_') && depth < MAX_DEPTH) {
      const end = closing(src, i + 1, ch)
      const wordInside = ch === '_' ? /\w/.test(src[i - 1] ?? ' ') : false // snake_case stays text
      if (end > i + 1 && !wordInside && src[i + 1] !== ' ') { out.push({ t: 'em', c: parseInline(src.slice(i + 1, end), depth + 1) }); i = end + 1; continue }
    }
    if ((ch === 'h' || ch === 'H') && /^https?:\/\//i.test(src.slice(i, i + 8)) && (i === 0 || /[\s(]/.test(src[i - 1]))) {
      const m = /^https?:\/\/[^\s<>"')\]]+[^\s<>"')\].,;:!?]/i.exec(src.slice(i))
      const href = m ? safeHref(m[0]) : null
      if (m && href) { out.push({ t: 'link', href, c: [{ t: 'text', v: m[0] }] }); i += m[0].length; continue }
    }
    pushText(out, ch)
    i++
  }
  return out
}

/** One line of inline markdown (the pinned announcement). */
export function parseLine(src: string): Inline[] {
  return parseInline(src.replace(/\s*\n\s*/g, ' ').trim())
}

export function parseMarkdown(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, '\n').split('\n')
  const blocks: Block[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length > 0) blocks.push({ t: 'p', c: parseInline(para.join('\n')) })
    para = []
  }
  for (let n = 0; n < lines.length; n++) {
    const line = lines[n]
    if (line.trim() === '') { flush(); continue }
    const heading = /^(#{2,3})\s+(.+)$/.exec(line)
    if (heading) { flush(); blocks.push({ t: 'h', level: heading[1].length === 2 ? 2 : 3, c: parseInline(heading[2].trim()) }); continue }
    if (/^>\s?/.test(line)) {
      flush()
      const quoted = [line.replace(/^>\s?/, '')]
      while (n + 1 < lines.length && /^>\s?/.test(lines[n + 1])) quoted.push(lines[++n].replace(/^>\s?/, ''))
      blocks.push({ t: 'quote', c: parseInline(quoted.join('\n')) })
      continue
    }
    const ul = /^\s*[-*+]\s+(.+)$/.exec(line)
    const ol = /^\s*\d{1,3}[.)]\s+(.+)$/.exec(line)
    if (ul || ol) {
      flush()
      const ordered = !!ol
      const items: Inline[][] = [parseInline((ul ?? ol)![1])]
      const same = ordered ? /^\s*\d{1,3}[.)]\s+(.+)$/ : /^\s*[-*+]\s+(.+)$/
      while (n + 1 < lines.length && same.test(lines[n + 1])) items.push(parseInline(same.exec(lines[++n])![1]))
      blocks.push(ordered ? { t: 'ol', items } : { t: 'ul', items })
      continue
    }
    para.push(line)
  }
  flush()
  return blocks
}
