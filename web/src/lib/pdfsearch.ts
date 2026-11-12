// Search extracted PDF text while retaining the offsets PDF.js uses for its
// text layer. This lets matches span font changes, ligatures and line breaks.
export type PdfTextItem = { str: string; hasEOL?: boolean }
type Position = { item: number; from: number; to: number }
export type PdfTextIndex = { text: string; positions: (Position | null)[] }
export type PdfMatch = { page: number; segments: Position[] }
export const emptyPdfMatches: PdfMatch[] = []

export function groupPdfMatches(matches: PdfMatch[]): Map<number, PdfMatch[]> {
  const pages = new Map<number, PdfMatch[]>()
  for (const match of matches) {
    const group = pages.get(match.page)
    if (group) group.push(match)
    else pages.set(match.page, [match])
  }
  return pages
}

// Bound the work in flight so closing search stops queuing a long document.
// Results retain page order even when worker replies arrive out of order.
export async function indexPdfPages(
  count: number,
  read: (page: number) => Promise<PdfTextItem[]>,
  cancelled: () => boolean,
): Promise<PdfTextIndex[] | null> {
  const pages: PdfTextIndex[] = []
  let next = 0
  let stopped = false
  async function worker() {
    while (!stopped && !cancelled() && next < count) {
      const index = next++
      const items = await read(index + 1)
      if (!cancelled()) pages[index] = indexPdfText(items)
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(8, count) }, worker))
  } catch (error) {
    stopped = true
    throw error
  }
  return cancelled() ? null : pages
}

export function indexPdfText(items: PdfTextItem[]): PdfTextIndex {
  const chars: { char: string; position: Position | null }[] = []
  items.forEach((item, n) => {
    let offset = 0
    for (const char of item.str) {
      chars.push({ char, position: { item: n, from: offset, to: offset + char.length } })
      offset += char.length
    }
    if (item.hasEOL) chars.push({ char: '\n', position: null })
  })
  const out: string[] = []
  const positions: (Position | null)[] = []
  for (let i = 0; i < chars.length; i++) {
    const { char, position } = chars[i]
    // A printed line-wrap hyphen is not part of the searchable word.
    if (char === '-' && i > 0 && /\p{L}/u.test(chars[i - 1].char)) {
      let end = i + 1
      let newline = false
      while (end < chars.length && /\s/u.test(chars[end].char)) {
        newline ||= chars[end].char === '\n'
        end++
      }
      if (newline && end < chars.length && /\p{L}/u.test(chars[end].char)) {
        i = end - 1
        continue
      }
    }
    const normalized = /\s/u.test(char) ? ' ' : char.normalize('NFKD').toLowerCase()
    if (normalized === ' ' && out.at(-1) === ' ') continue
    out.push(normalized)
    for (let j = 0; j < normalized.length; j++) positions.push(position)
  }
  return { text: out.join(''), positions }
}

export function findPdfMatches(pages: PdfTextIndex[], query: string): PdfMatch[] {
  const needle = query.normalize('NFKD').toLowerCase().replace(/\s+/gu, ' ').trim()
  if (!needle) return []
  const matches: PdfMatch[] = []
  pages.forEach((page, n) => {
    for (let start = page.text.indexOf(needle); start >= 0; start = page.text.indexOf(needle, start + needle.length)) {
      const segments: Position[] = []
      for (let i = start; i < start + needle.length; i++) {
        const position = page.positions[i]
        if (!position) continue
        const last = segments.at(-1)
        if (last?.item === position.item) last.to = Math.max(last.to, position.to)
        else segments.push({ ...position })
      }
      if (segments.length) matches.push({ page: n + 1, segments })
    }
  })
  return matches
}
