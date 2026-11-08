import { latexWords, maskForSections } from './wordcount.ts'

// The document outline: the headings of the main file and of the files it
// \input or \include, in reading order.

export type Heading = {
  level: number // 0 \part … 5 \paragraph
  title: string
  path: string // project-relative file the heading is in
  line: number // 1-based
}
// Where, in a file, the outline stands: from `line` on, the heading at `index`
// (-1: none yet) is the one containing the text.
export type Mark = { line: number; index: number }
export type Outline = { headings: Heading[]; marks: Record<string, Mark[]> }

export const emptyOutline: Outline = { headings: [], marks: {} }

const levels: Record<string, number> = {
  part: 0,
  chapter: 1,
  section: 2,
  subsection: 3,
  subsubsection: 4,
  paragraph: 5,
}
// One level of nested braces in the title: \section{The \emph{best} way}.
const heading =
  /\\(part|chapter|section|subsection|subsubsection|paragraph)\*?\s*(?:\[[^\]]*\])?\s*\{([^{}]*(?:\{[^{}]*\}[^{}]*)*)\}/g
// \include{x}, \input{x} and the unbraced \input x. \inputenc, \includegraphics… don't match.
const include = /\\(?:input|include)\s*\{([^{}]*)\}|\\input\s+([^\s{}%\\]+)/g

type Item = { at: number; heading?: Omit<Heading, 'path' | 'line'>; include?: string }

function lineAt(source: string, offset: number): number {
  let n = 1
  for (let i = source.indexOf('\n'); i >= 0 && i < offset; i = source.indexOf('\n', i + 1)) n++
  return n
}

// Headings and includes of one file, in source order. Comments, verbatim and
// macro definitions are masked like the word counter does.
function scan(source: string): Item[] {
  const clean = maskForSections(source)
  const items: Item[] = []
  for (const m of clean.matchAll(heading)) {
    const title =
      latexWords(m[2])
        .map((w) => w.text)
        .join(' ') || m[2].trim()
    items.push({ at: m.index, heading: { level: levels[m[1]], title } })
  }
  for (const m of clean.matchAll(include)) {
    const name = (m[1] ?? m[2]).trim()
    if (name) items.push({ at: m.index, include: name })
  }
  return items.sort((a, b) => a.at - b.at)
}

export function headingsIn(source: string, path: string): Heading[] {
  return scan(source)
    .filter((i) => i.heading)
    .map((i) => ({ ...i.heading!, path, line: lineAt(source, i.at) }))
}

/**
 * The project path TeX opens for \input{name}: relative to the main file's
 * folder (the compile directory), with .tex added when there is no extension.
 * null when it leaves the project.
 */
export function resolveInclude(mainFile: string, name: string): string | null {
  if (name.startsWith('/')) return null
  const file = /\.[A-Za-z0-9]+$/.test(name.split('/').pop()!) ? name : `${name}.tex`
  const dir = mainFile.includes('/') ? mainFile.slice(0, mainFile.lastIndexOf('/')) : ''
  const parts: string[] = []
  for (const p of `${dir}/${file}`.split('/')) {
    if (p === '' || p === '.') continue
    if (p === '..') {
      if (!parts.length) return null
      parts.pop()
    } else parts.push(p)
  }
  return parts.join('/')
}

const maxDepth = 16

/**
 * Builds the outline from mainFile, following \input and \include. Each file
 * is expanded once (no cycles); files read returns null for are skipped.
 */
export async function buildOutline(mainFile: string, read: (path: string) => Promise<string | null>): Promise<Outline> {
  const out: Outline = { headings: [], marks: {} }
  const seen = new Set<string>()
  async function walk(path: string, depth: number) {
    if (seen.has(path) || depth > maxDepth) return
    seen.add(path)
    const source = await read(path)
    if (source == null) return
    const marks: Mark[] = [{ line: 1, index: out.headings.length - 1 }]
    out.marks[path] = marks
    for (const item of scan(source)) {
      const line = lineAt(source, item.at)
      if (item.heading) {
        out.headings.push({ ...item.heading, path, line })
        marks.push({ line, index: out.headings.length - 1 })
      } else if (item.include) {
        const target = resolveInclude(mainFile, item.include)
        if (!target) continue
        await walk(target, depth + 1)
        // After the \input line, the last heading it brought in contains the text.
        marks.push({ line: line + 1, index: out.headings.length - 1 })
      }
    }
  }
  await walk(mainFile, 0)
  return out
}

/** Index of the heading that contains line of path, or -1. */
export function currentHeading(outline: Outline, path: string, line: number): number {
  const marks = outline.marks[path]
  if (!marks) return -1
  let index = -1
  for (const m of marks) {
    if (m.line > line) break
    index = m.index
  }
  return index
}

/**
 * Each heading's depth in the tree: how many headings above it are still open
 * (have a lower level). Skipped levels add no empty indent: a \paragraph right
 * under a \section is one step in.
 */
export function depths(headings: Heading[]): number[] {
  const open: number[] = []
  return headings.map((h) => {
    while (open.length && open[open.length - 1] >= h.level) open.pop()
    open.push(h.level)
    return open.length - 1
  })
}
