import assert from 'node:assert/strict'
import { test } from 'node:test'

import { compileView, pdfChanged, type Activity, type Downloads } from './compile-state.ts'

const none: Downloads = { fetched: 0, missing: [], offline: false }
const act = (a: Partial<Activity>): Activity => ({ state: 'compiling', file: '', downloaded: 0, retrying: false, ...a })

test('a running compile is plain compiling until downloads start', () => {
  assert.deepEqual(compileView({ compiling: true, activity: null, result: null }), { kind: 'compiling' })
  assert.deepEqual(compileView({ compiling: true, activity: act({}), result: null }), { kind: 'compiling' })
})

test('downloads show the file and the count', () => {
  const activity = act({ state: 'downloading', file: 'siunitx.sty', downloaded: 3 })
  assert.deepEqual(compileView({ compiling: true, activity, result: null }), {
    kind: 'downloading',
    file: 'siunitx.sty',
    count: 3,
  })
})

test('a retry wins over the download', () => {
  const activity = act({ state: 'downloading', file: 'a.sty', downloaded: 1, retrying: true })
  assert.deepEqual(compileView({ compiling: true, activity, result: null }), { kind: 'retrying' })
})

test('the activity is ignored once the compile ended', () => {
  const activity = act({ state: 'downloading', file: 'a.sty', downloaded: 1 })
  assert.deepEqual(compileView({ compiling: false, activity, result: { downloads: none } }), { kind: 'idle' })
})

test('missing files need a download', () => {
  const downloads = { ...none, missing: ['siunitx.sty'], offline: true }
  assert.deepEqual(compileView({ compiling: false, activity: null, result: { downloads } }), {
    kind: 'needs-download',
    files: ['siunitx.sty'],
  })
})

test('offline with no file names still needs a download', () => {
  const downloads = { ...none, offline: true }
  assert.deepEqual(compileView({ compiling: false, activity: null, result: { downloads } }), {
    kind: 'needs-download',
    files: [],
  })
})

test('a timeout while downloading is an interruption', () => {
  const downloads = { ...none, fetched: 4, interrupted: true }
  assert.deepEqual(compileView({ compiling: false, activity: null, result: { downloads } }), { kind: 'interrupted' })
})

test('a successful compile that fetched files says how many', () => {
  assert.deepEqual(compileView({ compiling: false, activity: null, result: { downloads: { ...none, fetched: 12 } } }), {
    kind: 'downloaded',
    count: 12,
  })
})

test('older or superseded results show nothing special', () => {
  assert.deepEqual(compileView({ compiling: false, activity: null, result: {} }), { kind: 'idle' })
  assert.deepEqual(
    compileView({
      compiling: false,
      activity: null,
      result: { superseded: true, downloads: { ...none, offline: true } },
    }),
    { kind: 'idle' },
  )
})

test('the PDF viewer reloads only when the PDF is new', () => {
  assert.equal(pdfChanged({ pdf: '.refract/build/main.pdf', downloads: none }), true)
  assert.equal(pdfChanged({ pdf: '.refract/build/main.pdf' }), true)
  assert.equal(pdfChanged({ pdf: '.refract/build/main.pdf', downloads: { ...none, missing: ['a.sty'] } }), false)
  assert.equal(pdfChanged({ downloads: none }), false)
})
