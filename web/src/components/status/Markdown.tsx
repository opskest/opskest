// Renders the safe markdown subset (lib/safeMarkdown.ts) as React elements.
// There is no dangerouslySetInnerHTML: every string reaches the DOM as text.

import type { ReactNode } from 'react'
import { parseLine, parseMarkdown, type Inline } from '../../lib/safeMarkdown'

function renderInline(nodes: Inline[], keyPrefix = 'i'): ReactNode[] {
  return nodes.map((n, i) => {
    const key = `${keyPrefix}${i}`
    switch (n.t) {
      case 'text': return n.v
      case 'br': return <br key={key} />
      case 'code': return <code key={key} className="font-mono text-[0.92em] px-1 py-0.5 rounded-md bg-surface-2 border border-default">{n.v}</code>
      case 'strong': return <strong key={key} className="font-semibold">{renderInline(n.c, key)}</strong>
      case 'em': return <em key={key}>{renderInline(n.c, key)}</em>
      case 'image': return <img key={key} src={n.src} alt={n.alt} loading="lazy" referrerPolicy="no-referrer" className="inline-block max-h-20 max-w-full align-middle rounded-md" />
      case 'link': {
        const external = n.href.startsWith('http')
        return (
          <a key={key} href={n.href} {...(external ? { target: '_blank', rel: 'noopener noreferrer nofollow' } : {})} className="text-accent underline underline-offset-2 hover:no-underline break-words">
            {renderInline(n.c, key)}
          </a>
        )
      }
    }
  })
}

/** One line (the pinned announcement): inline formatting only. */
export function InlineMarkdown({ source }: { source: string }) {
  return <>{renderInline(parseLine(source))}</>
}

export function Markdown({ source, className = '' }: { source: string; className?: string }) {
  const blocks = parseMarkdown(source)
  if (blocks.length === 0) return null
  return (
    <div className={`flex flex-col gap-2.5 leading-relaxed break-words ${className}`}>
      {blocks.map((b, i) => {
        switch (b.t) {
          case 'p': return <p key={i}>{renderInline(b.c, `b${i}-`)}</p>
          case 'h': return b.level === 2
            ? <h3 key={i} className="text-[14px] font-semibold text-body">{renderInline(b.c, `b${i}-`)}</h3>
            : <h4 key={i} className="text-[13px] font-semibold text-body">{renderInline(b.c, `b${i}-`)}</h4>
          case 'quote': return <blockquote key={i} className="border-l-2 border-default pl-3 text-muted">{renderInline(b.c, `b${i}-`)}</blockquote>
          case 'ul': return <ul key={i} className="list-disc pl-5 flex flex-col gap-1">{b.items.map((it, j) => <li key={j}>{renderInline(it, `b${i}-${j}-`)}</li>)}</ul>
          case 'ol': return <ol key={i} className="list-decimal pl-5 flex flex-col gap-1">{b.items.map((it, j) => <li key={j}>{renderInline(it, `b${i}-${j}-`)}</li>)}</ol>
        }
      })}
    </div>
  )
}
