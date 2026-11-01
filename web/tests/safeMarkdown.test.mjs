import test from 'node:test'
import assert from 'node:assert/strict'
import { parseInline, parseLine, parseMarkdown, safeHref, safeImage } from '../src/lib/safeMarkdown.ts'

const flat = nodes => JSON.stringify(nodes)

test('links are limited to http, https and mailto', () => {
  for (const ok of ['https://example.com/a?b=c', 'http://example.com', 'mailto:ops@example.com', 'HTTPS://Example.com']) assert.ok(safeHref(ok), ok)
  for (const bad of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,hi', 'vbscript:x', '/login', '//evil.example', 'ftp://example.com', 'https://', 'https://exa mple.com', 'java\nscript:alert(1)', 'mailto:', '']) {
    assert.equal(safeHref(bad), null, bad)
  }
})

test('images must be https', () => {
  assert.ok(safeImage('https://cdn.example.com/a.png'))
  assert.equal(safeImage('http://cdn.example.com/a.png'), null)
  assert.equal(safeImage('data:image/png;base64,AAAA'), null)
  assert.equal(safeImage('javascript:alert(1)'), null)
})

test('an unsafe link is shown as text, never as a link', () => {
  const nodes = parseInline('[click](javascript:alert(1)) and [ok](https://example.com)')
  assert.equal(nodes.filter(n => n.t === 'link').length, 1)
  assert.ok(flat(nodes).includes('javascript:alert(1)'), 'the text is kept')
  assert.ok(!flat(nodes).includes('"href":"javascript'))
})

test('raw HTML is plain text', () => {
  const blocks = parseMarkdown('<img src=x onerror=alert(1)> <script>alert(1)</script>')
  assert.equal(blocks.length, 1)
  assert.deepEqual(blocks[0].c.map(n => n.t), ['text'])
  assert.ok(blocks[0].c[0].v.includes('<script>'))
})

test('emphasis, code and escapes', () => {
  const bs = String.fromCharCode(92)
  assert.deepEqual(parseInline('**b** *i* `c` ' + bs + '*x' + bs + '*').map(n => n.t), ['strong', 'text', 'em', 'text', 'code', 'text'])
  assert.equal(parseInline('snake_case_name')[0].t, 'text')
  assert.equal(parseInline('snake_case_name').length, 1)
})

test('bare addresses become links, trailing punctuation stays outside', () => {
  const nodes = parseInline('See https://example.com/status.')
  const link = nodes.find(n => n.t === 'link')
  assert.equal(link.href, 'https://example.com/status')
  assert.equal(nodes.at(-1).v, '.')
})

test('blocks: paragraphs, lists, headings, quotes', () => {
  const blocks = parseMarkdown('## Title\n\nfirst\nsecond\n\n- a\n- b\n\n1. one\n2. two\n\n> quoted')
  assert.deepEqual(blocks.map(b => b.t), ['h', 'p', 'ul', 'ol', 'quote'])
  assert.equal(blocks[2].items.length, 2)
})

test('unterminated syntax never throws or loops', () => {
  for (const s of ['**', '[', '![', '[a](', '`', '*a', '_', '[[[[[[[[[[x](https://a.b)', '*'.repeat(500), '['.repeat(500) + ']('.repeat(500)]) {
    assert.ok(Array.isArray(parseInline(s)))
    assert.ok(Array.isArray(parseMarkdown(s)))
  }
})

test('the announcement is a single line', () => {
  assert.ok(!flat(parseLine('a\nb')).includes('"br"'))
})
