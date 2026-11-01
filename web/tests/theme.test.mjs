import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

const bootstrap = readFileSync(new URL('../public/theme-init.js', import.meta.url), 'utf8')
let instance = 0

async function environment(saved = null, blocked = false) {
  const classes = new Set()
  const classList = { add: value => classes.add(value), remove: (...values) => values.forEach(value => classes.delete(value)) }
  const storage = {
    getItem: () => { if (blocked) throw new Error('Blocked'); return saved },
    setItem: (_key, value) => { if (blocked) throw new Error('Blocked'); saved = value },
  }
  globalThis.window = new EventTarget()
  window.localStorage = storage
  globalThis.document = { documentElement: { classList } }
  const module = await import(`../src/lib/theme.ts?test=${instance++}`)
  return { module, classes, storage, readSaved: () => saved }
}

test('first paint and runtime agree for new, saved, invalid, and blocked preferences', async () => {
  for (const [saved, blocked, expected] of [[null, false, 'dark'], ['invalid', false, 'dark'], ['light', false, 'light'], ['dark', false, 'dark'], ['system', false, 'system'], [null, true, 'dark']]) {
    const env = await environment(saved, blocked)
    vm.runInNewContext(bootstrap, { document, localStorage: env.storage })
    assert.equal(env.module.getTheme(), expected)
    assert.deepEqual([...env.classes], expected === 'system' ? [] : [expected])
  }
})

test('two controls share a preference and notify only mounted subscribers', async () => {
  const { module, classes, readSaved } = await environment()
  let first = 0, second = 0
  const unsubscribe = module.subscribeTheme(() => first++)
  module.subscribeTheme(() => second++)
  module.setTheme('light')
  assert.equal(module.getTheme(), 'light')
  assert.equal(readSaved(), 'light')
  assert.deepEqual([...classes], ['light'])
  assert.equal(first, 1)
  assert.equal(second, 1)
  unsubscribe()
  module.setTheme('system')
  assert.equal(first, 1)
  assert.equal(second, 2)
  assert.equal(classes.size, 0)
})

test('blocked storage allows a session-only appearance choice', async () => {
  const { module, classes } = await environment(null, true)
  assert.equal(module.getTheme(), 'dark')
  assert.doesNotThrow(() => module.setTheme('light'))
  assert.equal(module.getTheme(), 'light')
  assert.deepEqual([...classes], ['light'])
})

test('another tab updates preferences even between mounted controls', async () => {
  const { module, storage, classes } = await environment('dark')
  const unsubscribe = module.subscribeTheme(() => {})
  unsubscribe()
  window.dispatchEvent(Object.assign(new Event('storage'), { key: 'opskest-theme', newValue: 'light', storageArea: storage }))
  assert.equal(module.getTheme(), 'light')
  assert.deepEqual([...classes], ['light'])
  window.dispatchEvent(Object.assign(new Event('storage'), { key: null, newValue: null, storageArea: storage }))
  assert.equal(module.getTheme(), 'dark')
})

test('unrelated storage events do not change appearance', async () => {
  const { module, storage } = await environment('dark')
  module.getTheme()
  window.dispatchEvent(Object.assign(new Event('storage'), { key: 'other', newValue: 'light', storageArea: storage }))
  assert.equal(module.getTheme(), 'dark')
  window.dispatchEvent(Object.assign(new Event('storage'), { key: 'opskest-theme', newValue: 'light', storageArea: {} }))
  assert.equal(module.getTheme(), 'dark')
})

test('temporary status-page overrides preserve the preference for restoration', async () => {
  const { module, classes, readSaved } = await environment('dark')
  module.getTheme()
  module.applyTheme('light')
  assert.deepEqual([...classes], ['light'])
  assert.equal(readSaved(), 'dark')
  module.applyTheme(module.getTheme())
  assert.deepEqual([...classes], ['dark'])
})
