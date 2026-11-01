import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import crypto from 'node:crypto'
import { BRAND_ASSETS, DARK_PLATE_BRANDS, PROVIDER_CATEGORIES, PROVIDER_PRESETS, providerBrand } from '../src/lib/providers.ts'

test('every curated provider has a unique secure feed and a category', () => {
  assert.ok(PROVIDER_PRESETS.length >= 99)
  assert.equal(new Set(PROVIDER_PRESETS.map(p => p.url)).size, PROVIDER_PRESETS.length)
  assert.equal(new Set(PROVIDER_PRESETS.map(p => p.name)).size, PROVIDER_PRESETS.length)
  for (const preset of PROVIDER_PRESETS) {
    assert.equal(new URL(preset.url).protocol, 'https:', preset.name)
    assert.equal(providerBrand(preset.url), preset.brand, preset.name)
    assert.ok(PROVIDER_CATEGORIES.includes(preset.category), `${preset.name}: unknown category ${preset.category}`)
  }
})

test('every brand marked as having a logo ships its asset; the rest fall back to initials', () => {
  const provenance = JSON.parse(fs.readFileSync(new URL('../public/providers/sources.json', import.meta.url), 'utf8'))
  for (const [slug, ext] of Object.entries(BRAND_ASSETS)) {
    const asset = provenance.icons.find(i => i.slug === slug)
    assert.ok(asset, `missing asset source: ${slug}`)
    assert.ok(asset.file.endsWith('.' + ext), `${slug}: file type does not match`)
    assert.ok(fs.existsSync(new URL('../public/providers/' + asset.file, import.meta.url)), slug)
  }
  // Every provider that existed before the catalog grew still has its logo.
  assert.ok(Object.keys(BRAND_ASSETS).length >= 82)
})

test('provider identity rejects lookalike domains and preserves distinct product marks', () => {
  for (const url of ['https://githubstatus.com.evil.test/', 'https://status.digitalocean.com.attacker.test/', 'https://notgoogle.com/appsstatus/', 'not a URL']) assert.equal(providerBrand(url), null)
  assert.equal(providerBrand('https://metastatus.com/whatsapp-business-api'), 'whatsapp')
  assert.equal(providerBrand('https://metastatus.com/graph-api'), 'meta')
  assert.equal(providerBrand('https://status.firebase.google.com/'), 'firebase')
  assert.equal(providerBrand('https://status.cloud.google.com/'), 'googlecloud')
})

// ---- logo coverage, provenance and safety -------------------------------------------------

/** Providers with no licensed mark we could ship: they render a deterministic monogram tile. */
const MONOGRAM_ONLY = ['dnsimple', 'imgix', 'plaid', 'workos', 'duo', 'launchdarkly', 'klaviyo', 'customerio', 'ably', 'honeycomb', 'logrocket', 'segment', 'amplitude', 'cohere', 'pinecone', 'mux']
const dir = new URL('../public/providers/', import.meta.url)
const sources = JSON.parse(fs.readFileSync(new URL('sources.json', dir), 'utf8'))
const logoFiles = fs.readdirSync(dir).filter(f => /\.(svg|png)$/.test(f))
const lum = hex => {
  const c = [0, 2, 4].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(v => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
}
const contrast = (a, b) => { const [x, y] = [lum(a), lum(b)]; return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05) }

test('every preset resolves to a logo file on disk or is in the explicit monogram allowlist', () => {
  for (const preset of PROVIDER_PRESETS) {
    const brand = providerBrand(preset.url)
    assert.equal(brand, preset.brand, preset.name)
    if (MONOGRAM_ONLY.includes(brand)) {
      assert.equal(BRAND_ASSETS[brand], undefined, `${preset.name}: listed as monogram-only but has an asset`)
      continue
    }
    const ext = BRAND_ASSETS[brand]
    assert.ok(ext, `${preset.name} (${brand}) has no logo and is not an allowed monogram provider`)
    assert.ok(fs.existsSync(new URL(`${brand}.${ext}`, dir)), `${brand}.${ext} is missing on disk`)
  }
  for (const brand of MONOGRAM_ONLY) assert.ok(PROVIDER_PRESETS.some(p => p.brand === brand), `${brand} is allowlisted but not a preset`)
  assert.ok(MONOGRAM_ONLY.length <= 16)
})

test('every logo file is referenced, has provenance and a matching hash', () => {
  for (const file of logoFiles) {
    const brand = file.replace(/\.(svg|png)$/, '')
    assert.equal(BRAND_ASSETS[brand], file.slice(brand.length + 1), `${file} is not referenced by BRAND_ASSETS`)
    const entry = sources.icons.find(i => i.file === file)
    assert.ok(entry, `${file} has no sources.json entry`)
    assert.equal(entry.slug, brand)
    assert.ok(entry.source && /^https:\/\//.test(entry.source), `${file}: source URL`)
    assert.ok(entry.sha256 && /^[0-9a-f]{64}$/.test(entry.sha256), `${file}: sha256`)
    const size = fs.statSync(new URL(file, dir)).size
    assert.ok(size < 20 * 1024 || file.endsWith('.png'), `${file} is ${size} bytes`)
    if (entry.collection === 'Simple Icons') {
      assert.equal(entry.license, 'CC0-1.0', file)
      assert.match(entry.collectionVersion, /^\d+\.\d+\.\d+$/, file)
      assert.equal(crypto.createHash('sha256').update(fs.readFileSync(new URL(file, dir))).digest('hex'), entry.sha256, `${file}: hash differs from the recorded one`)
    }
  }
  for (const brand of Object.keys(BRAND_ASSETS)) assert.ok(logoFiles.includes(`${brand}.${BRAND_ASSETS[brand]}`), `${brand} asset missing`)
  const notice = fs.readFileSync(new URL('NOTICE.md', dir), 'utf8')
  assert.match(notice, /CC0-1\.0/)
  for (const version of new Set(sources.icons.map(i => i.collectionVersion).filter(Boolean))) assert.ok(notice.includes(version), `NOTICE.md does not mention Simple Icons ${version}`)
})

test('SVG logos are inert: no script, foreignObject, event handlers or external references', () => {
  for (const file of logoFiles.filter(f => f.endsWith('.svg'))) {
    const svg = fs.readFileSync(new URL(file, dir), 'utf8')
    assert.match(svg, /^<svg[\s>]/, `${file}: root`)
    assert.ok(!/<script|foreignObject|<style|<image|<use|<iframe|<!ENTITY|<!DOCTYPE|javascript:|data:|url\(|xlink:|\shref\s*=|@import/i.test(svg), `${file}: unsafe construct`)
    assert.ok(!/\son[a-z]+\s*=/i.test(svg), `${file}: event handler attribute`)
    assert.ok(!/https?:\/\/(?!www\.w3\.org\/(2000\/svg|1999\/xlink))/i.test(svg), `${file}: external URL`)
  }
})

test('every mark stays legible on the plate it is drawn on', () => {
  for (const file of logoFiles.filter(f => f.endsWith('.svg'))) {
    const brand = file.replace('.svg', '')
    const svg = fs.readFileSync(new URL(file, dir), 'utf8')
    const hex = (svg.match(/^<svg[^>]*\sfill="#([0-9a-fA-F]{6})"/) ?? svg.match(/fill="#([0-9a-fA-F]{6})"/))?.[1]
    assert.ok(hex, `${file}: needs a fill colour`)
    const dark = DARK_PLATE_BRANDS.has(brand)
    assert.ok(contrast(hex, dark ? '1f1f1f' : 'ffffff') >= 2.2, `${brand}: ${hex} is too faint on the ${dark ? 'dark' : 'white'} plate`)
    if (dark) assert.ok(contrast(hex, 'ffffff') < 2.5, `${brand} would be fine on white; drop it from DARK_PLATE_BRANDS`)
  }
  for (const brand of DARK_PLATE_BRANDS) assert.ok(BRAND_ASSETS[brand], `${brand} has no asset`)
})

test('host-spoof protection still holds for the new brands', () => {
  for (const url of ['https://status.heroku.com.evil.test/feed', 'https://evil.test/status.supabase.com/', 'https://notion-status.com.attacker.test/history.rss', 'https://status.docker.com.evil.test/', 'https://status.npmjs.org.evil.test/', 'https://xstatus.mongodb.com/']) assert.equal(providerBrand(url), null, url)
  assert.equal(providerBrand('https://status.heroku.com/feed'), 'heroku')
  assert.equal(providerBrand('https://www.notion-status.com/history.rss'), 'notion')
  assert.equal(providerBrand('https://status.docker.com/'), 'docker')
})
