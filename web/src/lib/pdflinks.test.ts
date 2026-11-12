import { test } from 'node:test'
import assert from 'node:assert/strict'
import { groupPdfLinkLines, indexPdfCitations, pdfLinkUrl, resolvePdfDestination } from './pdflinks.ts'

type Document = Parameters<typeof resolvePdfDestination>[0]
function document(rotated = false) {
  const calls: string[] = []
  const doc = {
    numPages: 3,
    getDestination: async (name: string) => {
      calls.push(name)
      return name === 'cite.example' ? [{ num: 42, gen: 0 }, { name: 'XYZ' }, 72, 250, null] : null
    },
    getPageIndex: async (ref: { num: number }) => {
      assert.equal(ref.num, 42)
      return 1
    },
    getPage: async () => ({
      view: [0, 0, 600, 800],
      getViewport: () => ({
        convertToViewportPoint: (x: number, y: number) => (rotated ? [800 - y, x] : [x, 800 - y]),
      }),
    }),
  } as unknown as Document
  return { doc, calls }
}

test('named citation destinations resolve page references and exact vertical positions', async () => {
  const { doc, calls } = document()
  assert.deepEqual(await resolvePdfDestination(doc, 'cite.example'), { page: 2, left: 72, top: 550 })
  assert.deepEqual(calls, ['cite.example'])
})

test('explicit destinations use zero-based page indexes and viewport coordinates', async () => {
  const { doc } = document()
  assert.deepEqual(await resolvePdfDestination(doc, [2, { name: 'XYZ' }, 50, 700, 2]), { page: 3, left: 50, top: 100 })
  assert.deepEqual(await resolvePdfDestination(doc, [0, { name: 'XYZ' }, null, null, null]), {
    page: 1,
    left: 0,
    top: 0,
  })
  for (const name of ['Fit', 'FitB']) {
    assert.deepEqual(await resolvePdfDestination(doc, [0, { name }]), { page: 1, left: 0, top: 0 })
  }
  for (const name of ['FitH', 'FitBH']) {
    assert.deepEqual(await resolvePdfDestination(doc, [0, { name }, 600]), { page: 1, left: 0, top: 200 })
  }
  for (const name of ['FitV', 'FitBV']) {
    assert.deepEqual(await resolvePdfDestination(doc, [0, { name }, 60]), { page: 1, left: 60, top: 0 })
  }
  assert.deepEqual(await resolvePdfDestination(doc, [1, { name: 'FitR' }, 60, 300, 200, 600]), {
    page: 2,
    left: 60,
    top: 200,
  })
  const rotated = document(true).doc
  assert.deepEqual(await resolvePdfDestination(rotated, [0, { name: 'FitH' }, 600]), { page: 1, left: 200, top: 0 })
  assert.deepEqual(await resolvePdfDestination(rotated, [0, { name: 'FitV' }, 60]), { page: 1, left: 0, top: 60 })
  assert.deepEqual(await resolvePdfDestination(rotated, [0, { name: 'XYZ' }, 50, 700]), { page: 1, left: 100, top: 50 })
  assert.deepEqual(await resolvePdfDestination(rotated, [0, { name: 'FitR' }, 60, 300, 200, 600]), {
    page: 1,
    left: 200,
    top: 60,
  })
})

test('missing, unsupported and invalid destinations leave navigation unchanged', async () => {
  const { doc } = document()
  for (const target of [
    'missing',
    [],
    [null, { name: 'Fit' }],
    [-1, { name: 'Fit' }],
    [3, { name: 'Fit' }],
    [1.5, { name: 'Fit' }],
    [0, { name: 'Unknown' }],
    [0, { name: 'FitR' }, 1, 2],
    [0, { name: 'FitR' }, 0, 1, NaN, 2],
  ]) {
    assert.equal(await resolvePdfDestination(doc, target), null)
  }
})

test('PDF website and contact links only accept supported URL schemes', () => {
  assert.equal(pdfLinkUrl('https://example.com/path'), 'https://example.com/path')
  assert.equal(pdfLinkUrl('mailto:you@example.com'), 'mailto:you@example.com')
  assert.equal(pdfLinkUrl('tel:+123456'), 'tel:+123456')
  for (const url of ['javascript:alert(1)', 'data:text/html,hello', 'file:///tmp/document', '/relative', undefined, 42])
    assert.equal(pdfLinkUrl(url), undefined)
})

test('wrapped annotations share a highlight without joining separate entries or repeated citations', () => {
  const line = (id: string, top: number, left = 10, dest = 'figure.1', width = 60) => ({
    id,
    left,
    top,
    width,
    height: 2,
    dest,
  })
  const grouped = groupPdfLinkLines([
    line('first', 10),
    line('middle', 13),
    line('last', 16, 10, 'figure.1', 25),
    line('other-entry', 19, 10, 'figure.2'),
    line('repeated', 25),
    line('citation-a', 30, 10, 'cite.example', 2),
    line('citation-b', 30, 40, 'cite.example', 2),
  ])
  assert.deepEqual(
    grouped.map((link) => link.group),
    ['first', 'first', 'first', 'other-entry', 'repeated', 'citation-a', 'citation-b'],
  )
  assert.equal(grouped[2].width, 25)
  assert.equal(grouped[2].dest, 'figure.1')
})

test('citation indexing groups wrapped links, retains distinct occurrences and excludes non-citations', async () => {
  const annotation = (id: string, dest: string, rect: number[]) => ({ id, dest, rect, annotationType: 2 })
  const doc = {
    numPages: 3,
    getPageIndex: async () => 2,
    getDestination: async (name: string) =>
      name === 'cite.missing' ? null : [{ num: 42, gen: 0 }, { name: 'XYZ' }, 72, 700, null],
    getPage: async (n: number) => ({
      view: [0, 0, 600, 800],
      getViewport: () => ({ convertToViewportPoint: (x: number, y: number) => [x, 800 - y] }),
      getAnnotations: async () =>
        n === 1
          ? [
              annotation('a', 'cite.example', [72, 700, 200, 710]),
              annotation('a-wrap', 'cite.example', [72, 687, 140, 697]),
              annotation('b', 'cite.example', [72, 300, 90, 310]),
              annotation('section', 'section.1', [72, 200, 90, 210]),
              annotation('missing', 'cite.missing', [72, 100, 90, 110]),
            ]
          : n === 2
            ? [annotation('c', 'cite.example', [72, 700, 90, 710])]
            : [],
    }),
  } as unknown as Document
  const result = await indexPdfCitations(doc, () => false)
  assert.equal(result!.length, 1)
  assert.deepEqual(result![0].target, { page: 3, left: 72, top: 100 })
  assert.deepEqual(
    result![0].citations.map(({ page, top }) => ({ page, top })),
    [
      { page: 1, top: 90 },
      { page: 1, top: 490 },
      { page: 2, top: 90 },
    ],
  )
})

test('citation indexing stops scheduling new pages after cancellation', async () => {
  let reads = 0,
    cancelled = false
  const doc = {
    numPages: 100,
    getPage: async () => {
      reads++
      await Promise.resolve()
      cancelled = true
      return { getViewport: () => ({}), getAnnotations: async () => [] }
    },
  } as unknown as Document
  assert.equal(await indexPdfCitations(doc, () => cancelled), null)
  assert.equal(reads, 4)
})
