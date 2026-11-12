import { test } from 'node:test'
import assert from 'node:assert/strict'
import { findPdfMatches, indexPdfText } from './pdfsearch.ts'

test('search spans PDF text items and line breaks, retaining highlight offsets', () => {
  const page = indexPdfText([{ str: 'Some re' }, { str: 'sults', hasEOL: true }, { str: 'are clear.' }])
  assert.deepEqual(findPdfMatches([page], 'RESULTS are'), [
    {
      page: 1,
      segments: [
        { item: 0, from: 5, to: 7 },
        { item: 1, from: 0, to: 5 },
        { item: 2, from: 0, to: 3 },
      ],
    },
  ])
})

test('normalizes ligatures, accents and printed hyphenation without shifting source offsets', () => {
  const page = indexPdfText([{ str: 'A ﬂower café inter-', hasEOL: true }, { str: 'national study.' }])
  assert.deepEqual(findPdfMatches([page], 'flower')[0].segments, [{ item: 0, from: 2, to: 7 }])
  assert.equal(findPdfMatches([page], 'cafe\u0301').length, 1)
  assert.deepEqual(findPdfMatches([page], 'international')[0].segments, [
    { item: 0, from: 13, to: 18 },
    { item: 1, from: 0, to: 8 },
  ])
  assert.equal(findPdfMatches([indexPdfText([{ str: 'well-known' }])], 'wellknown').length, 0)
})

test('empty queries, no matches, non-BMP offsets, page ordering and literal regex characters', () => {
  const pages = [indexPdfText([{ str: '😀 x+y x+y' }]), indexPdfText([{ str: 'x+y' }])]
  assert.equal(findPdfMatches(pages, '  ').length, 0)
  assert.equal(findPdfMatches(pages, 'absent').length, 0)
  const matches = findPdfMatches(pages, 'x+y')
  assert.deepEqual(
    matches.map((m) => m.page),
    [1, 1, 2],
  )
  assert.deepEqual(matches[0].segments, [{ item: 0, from: 3, to: 6 }])
})
