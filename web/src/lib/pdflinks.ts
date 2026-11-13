import type { PDFDocumentProxy } from 'pdfjs-dist'

export type PdfDestination = string | unknown[]
export type PdfLocation = { page: number; left: number; top: number }
type Document = Pick<PDFDocumentProxy, 'numPages' | 'getDestination' | 'getPageIndex' | 'getPage'>

// Destinations use PDF coordinates (usually measured up from the bottom).
// Convert through the page viewport so rotation and cropped pages work too.
// Retain the reader's zoom; a link only changes their location.
export async function resolvePdfDestination(doc: Document, destination: PdfDestination): Promise<PdfLocation | null> {
  const dest = typeof destination === 'string' ? await doc.getDestination(destination) : destination
  if (!Array.isArray(dest) || dest.length < 2) return null
  const ref = dest[0]
  let index: number
  if (Number.isInteger(ref)) index = ref as number
  else if (ref && typeof ref === 'object' && Number.isInteger(ref.num) && Number.isInteger(ref.gen))
    index = await doc.getPageIndex(ref)
  else return null
  if (!Number.isInteger(index) || index < 0 || index >= doc.numPages) return null
  const kind = dest[1]?.name
  const page = await doc.getPage(index + 1)
  const viewport = page.getViewport({ scale: 1 })
  const number = (value: unknown, fallback: number) =>
    typeof value === 'number' && Number.isFinite(value) ? value : fallback
  let left = 0,
    top = 0
  switch (kind) {
    case 'XYZ':
      ;[left, top] = viewport.convertToViewportPoint(number(dest[2], page.view[0]), number(dest[3], page.view[3]))
      break
    case 'FitH':
    case 'FitBH':
      ;[left, top] = viewport.convertToViewportPoint(page.view[0], number(dest[2], page.view[3]))
      break
    case 'FitV':
    case 'FitBV':
      ;[left, top] = viewport.convertToViewportPoint(number(dest[2], page.view[0]), page.view[3])
      break
    case 'FitR': {
      if (!dest.slice(2, 6).every((value) => typeof value === 'number' && Number.isFinite(value)) || dest.length < 6)
        return null
      const start = viewport.convertToViewportPoint(dest[2], dest[3])
      const end = viewport.convertToViewportPoint(dest[4], dest[5])
      left = Math.min(start[0], end[0])
      top = Math.min(start[1], end[1])
      break
    }
    case 'Fit':
    case 'FitB':
      break
    default:
      return null
  }
  return { page: index + 1, left: Math.max(0, left), top: Math.max(0, top) }
}

export function pdfLinkUrl(value: unknown): string | undefined {
  if (typeof value !== 'string') return
  try {
    const url = new URL(value)
    if (['https:', 'http:', 'mailto:', 'tel:'].includes(url.protocol)) return url.href
  } catch {
    /* Ignore unsupported or malformed link targets. */
  }
}

type LinkArea = {
  id: string
  left: number
  top: number
  width: number
  height: number
  dest?: PdfDestination
  url?: string
}

// TeX emits a separate annotation for each wrapped line. Join neighbouring
// lines visually, while keeping each original hit area and destination.
// Distance matters: two citations to the same reference are separate links.
export function groupPdfLinkLines<T extends LinkArea>(links: T[]): (T & { group: string })[] {
  const groups: { key: string; id: string; areas: LinkArea[] }[] = []
  return links.map((link) => {
    const key = JSON.stringify(link.url ? ['url', link.url] : ['dest', link.dest])
    let group = groups.find(
      (group) =>
        group.key === key &&
        group.areas.some((area) => {
          const overlap = Math.min(area.left + area.width, link.left + link.width) - Math.max(area.left, link.left)
          const gap = Math.max(link.top - area.top - area.height, area.top - link.top - link.height)
          return overlap > 0 && gap <= Math.max(area.height, link.height) * 0.8
        }),
    )
    if (!group) {
      group = { key, id: link.id, areas: [] }
      groups.push(group)
    }
    group.areas.push(link)
    return { ...link, group: group.id }
  })
}

export type PdfCitation = PdfLocation & { width: number; height: number }
export type PdfBacklink = { destination: string; target: PdfLocation; citations: PdfCitation[] }

// Index links already embedded by hyperref/biblatex; no source rewrite or
// recompile is needed. Bounded work also lets document replacement stop a scan.
export async function indexPdfCitations(doc: Document, cancelled: () => boolean): Promise<PdfBacklink[] | null> {
  const sources: { destination: string; citation: PdfCitation }[][] = []
  async function parallel(count: number, visit: (index: number) => Promise<void>) {
    let next = 0
    let stopped = false
    await Promise.all(
      Array.from({ length: Math.min(4, count) }, async () => {
        try {
          while (!stopped && !cancelled() && next < count) await visit(next++)
        } catch (error) {
          stopped = true
          throw error
        }
      }),
    )
  }
  await parallel(doc.numPages, async (index) => {
    const page = await doc.getPage(index + 1)
    const viewport = page.getViewport({ scale: 1 })
    const areas: (LinkArea & { dest: string })[] = []
    for (const annotation of await page.getAnnotations({ intent: 'display' })) {
      if (annotation.annotationType !== 2 || typeof annotation.dest !== 'string' || !/^cite[.:]/i.test(annotation.dest))
        continue
      if (!Array.isArray(annotation.rect) || annotation.rect.length !== 4 || !annotation.rect.every(Number.isFinite))
        continue
      const start = viewport.convertToViewportPoint(annotation.rect[0], annotation.rect[1])
      const end = viewport.convertToViewportPoint(annotation.rect[2], annotation.rect[3])
      areas.push({
        id: annotation.id,
        dest: annotation.dest,
        left: Math.min(start[0], end[0]),
        top: Math.min(start[1], end[1]),
        width: Math.abs(end[0] - start[0]),
        height: Math.abs(end[1] - start[1]),
      })
    }
    const seen = new Set<string>()
    sources[index] = groupPdfLinkLines(areas)
      .filter((area) => {
        if (seen.has(area.group)) return false
        seen.add(area.group)
        return area.width > 0 && area.height > 0
      })
      .map((area) => ({
        destination: area.dest,
        citation: { page: index + 1, left: area.left, top: area.top, width: area.width, height: area.height },
      }))
  })
  if (cancelled()) return null
  const entries = new Map<string, PdfCitation[]>()
  for (const { destination, citation } of sources.flat()) {
    const citations = entries.get(destination)
    if (citations) citations.push(citation)
    else entries.set(destination, [citation])
  }
  const destinations = [...entries]
  const backlinks: PdfBacklink[] = []
  await parallel(destinations.length, async (index) => {
    const [destination, citations] = destinations[index]
    const target = await resolvePdfDestination(doc, destination)
    if (target && !cancelled())
      backlinks.push({
        destination,
        target,
        citations: citations.sort((a, b) => a.page - b.page || a.top - b.top || a.left - b.left),
      })
  })
  return cancelled() ? null : backlinks.sort((a, b) => a.target.page - b.target.page || a.target.top - b.target.top)
}
