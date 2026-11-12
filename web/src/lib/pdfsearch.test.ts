import { test } from 'node:test'
import assert from 'node:assert/strict'
import { findPdfMatches, indexPdfText, groupPdfMatches, indexPdfPages } from './pdfsearch.ts'

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

test('grouping keeps original match references and per-page lists', () => {
  const matches = findPdfMatches([indexPdfText([{ str: 'a a' }]), indexPdfText([{ str: 'a' }])], 'a')
  const groups = groupPdfMatches(matches)
  assert.deepEqual(groups.get(1), matches.slice(0, 2))
  assert.equal(groups.get(2)![0], matches[2])
  assert.equal(groups.get(3), undefined)
})

test('parallel indexing preserves page order with at most eight reads in flight', async () => {
  let active = 0,
    peak = 0
  const result = await indexPdfPages(
    20,
    async (page) => {
      peak = Math.max(peak, ++active)
      await new Promise((resolve) => setTimeout(resolve, page % 3))
      active--
      return [{ str: String(page) }]
    },
    () => false,
  )
  assert.equal(peak, 8)
  assert.deepEqual(
    result!.map((page) => page.text),
    Array.from({ length: 20 }, (_, i) => String(i + 1)),
  )
})

test('closing search stops scheduling pages and discards partial indexes', async () => {
  let cancelled = false,
    reads = 0
  const result = await indexPdfPages(
    100,
    async () => {
      reads++
      await Promise.resolve()
      cancelled = true
      return [{ str: 'text' }]
    },
    () => cancelled,
  )
  assert.equal(result, null)
  assert.equal(reads, 8)
  await assert.rejects(
    indexPdfPages(
      1,
      async () => {
        throw new Error('unreadable')
      },
      () => false,
    ),
    /unreadable/,
  )
})

test('real end-of-line hyphens share the documented wrapping ambiguity', () => {
  const page = indexPdfText([{ str: 'state-of-', hasEOL: true }, { str: 'the-art' }])
  assert.equal(findPdfMatches([page], 'state-ofthe-art').length, 1)
  assert.equal(findPdfMatches([page], 'state-of-the-art').length, 0)
})
