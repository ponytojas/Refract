import { test } from 'node:test'
import assert from 'node:assert/strict'
import { initialPdfSearch, pdfSearchReducer as reduce, pdfScrollRequest, pdfSearchMatches } from './pdfsearch-state.ts'
import { indexPdfText } from './pdfsearch.ts'
const pages = (text: string) => [indexPdfText([{ str: text }])]

test('compilation preserves and clamps selection without requesting navigation', () => {
  const doc = {},
    nextDoc = {},
    finalDoc = {}
  let state = reduce(initialPdfSearch, { type: 'show', doc })
  state = reduce(state, { type: 'query', doc, query: 'word' })
  state = reduce(state, { type: 'loaded', doc, pages: pages('word word word') })
  assert.ok(pdfScrollRequest(state, doc))
  state = reduce(state, { type: 'select', doc, index: 2 })
  assert.equal(pdfSearchMatches(state, nextDoc).length, 0)
  state = reduce(state, { type: 'loaded', doc: nextDoc, pages: pages('word word') })
  assert.equal(state.current, 1)
  assert.equal(pdfScrollRequest(state, nextDoc), undefined)
  state = reduce(state, { type: 'loaded', doc: finalDoc, pages: pages('word word word') })
  assert.equal(state.current, 1)
  assert.equal(pdfScrollRequest(state, finalDoc), undefined)
  state = reduce(state, { type: 'select', doc: finalDoc, index: 2 })
  assert.equal(pdfScrollRequest(state, finalDoc)?.match, state.matches[2])
})

test('index completion uses latest query; closing hides hits and reopening requests one jump', () => {
  const doc = {}
  let state = reduce(initialPdfSearch, { type: 'show', doc })
  state = reduce(state, { type: 'query', doc, query: 'old' })
  state = reduce(state, { type: 'query', doc, query: 'new' })
  state = reduce(state, { type: 'loaded', doc, pages: pages('old new new') })
  assert.equal(state.matches.length, 2)
  const id = pdfScrollRequest(state, doc)!.id
  state = reduce(state, { type: 'close' })
  assert.equal(pdfScrollRequest(state, doc), undefined)
  assert.equal(pdfSearchMatches(state, doc).length, 0)
  state = reduce(state, { type: 'show', doc })
  assert.ok(pdfScrollRequest(state, doc)!.id > id)
  state = reduce(state, { type: 'loaded', doc: {}, pages: pages('absent') })
  assert.equal(state.current, 0)
})
