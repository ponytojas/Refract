// Run with: node --test web/src/lib/windowDrag.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { dragStarts, installWindowDrag } from './windowDrag.ts'

// A tiny stand-in for Element: only what dragStarts uses.
class Fake {
  marks: string[]
  parent?: Fake
  constructor(marks: string[], parent?: Fake) {
    this.marks = marks
    this.parent = parent
  }
  closest(sel: string): Fake | null {
    const wanted = sel.split(',').map((s) => s.trim())
    for (let n = this as Fake | undefined; n; n = n.parent) {
      if (n.marks.some((m) => wanted.includes(m))) return n
    }
    return null
  }
}
Object.assign(globalThis, { Element: Fake })

test('empty parts of a drag region start a drag', () => {
  const bar = new Fake(['[data-window-drag]'])
  assert.equal(dragStarts(bar, 0), true)
  assert.equal(dragStarts(new Fake([], bar), 0), true)
})

test('controls inside a drag region do not', () => {
  const bar = new Fake(['[data-window-drag]'])
  for (const m of ['button', 'a', 'input', '[role=tab]', '[data-no-drag]']) {
    assert.equal(dragStarts(new Fake([m], bar), 0), false, m)
    assert.equal(dragStarts(new Fake([], new Fake([m], bar)), 0), false, `inside ${m}`)
  }
})

test('outside a region, other buttons and non-elements do not', () => {
  assert.equal(dragStarts(new Fake([]), 0), false)
  assert.equal(dragStarts(new Fake(['[data-window-drag]']), 2), false)
  assert.equal(dragStarts(null, 0), false)
})

test('nothing is installed without the app handler', () => {
  assert.equal(installWindowDrag({} as Document, {} as Window), false)
})

test('a mousedown on a region posts one drag message', () => {
  let listener: ((e: unknown) => void) | undefined
  const posted: unknown[] = []
  const doc = { addEventListener: (_: string, l: (e: unknown) => void) => (listener = l) } as unknown as Document
  const win = { webkit: { messageHandlers: { refractWindow: { postMessage: (m: unknown) => posted.push(m) } } } }
  assert.equal(installWindowDrag(doc, win as unknown as Window), true)
  listener!({ target: new Fake(['[data-window-drag]']), button: 0, defaultPrevented: false })
  listener!({ target: new Fake(['button'], new Fake(['[data-window-drag]'])), button: 0, defaultPrevented: false })
  assert.deepEqual(posted, [{ type: 'drag' }])
})
