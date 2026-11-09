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
// The command up to the title's opening brace; the title itself is read by
// balanced(), so formatting can nest: \section{A \textbf{\emph{b}} c}.
const heading = /\\(part|chapter|section|subsection|subsubsection|paragraph)\*?\s*(?:\[[^\]]*\])?\s*\{/g
// \include{x}, \input{x} and the unbraced \input x. \inputenc, \includegraphics… don't match.
const include = /\\(?:input|include)\s*\{([^{}]*)\}|\\input\s+([^\s{}%\\]+)/g

type Item = { at: number; heading?: Omit<Heading, 'path' | 'line'>; include?: string }

// Index of the brace closing the one at open, or -1 when it never closes.
// Escaped braces (\{ \}) don't count.
function balanced(source: string, open: number): number {
  let depth = 0
  for (let i = open; i < source.length; i++) {
    const c = source[i]
    if (c === '\\') i++
    else if (c === '{') depth++
    else if (c === '}' && --depth === 0) return i
  }
  return -1
}

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
    const open = m.index + m[0].length - 1
    const close = balanced(clean, open)
    if (close < 0) continue
    const arg = clean.slice(open + 1, close)
    const title =
      latexWords(arg)
        .map((w) => w.text)
        .join(' ') || arg.trim()
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

/**
 * Contents of files the outline reads from the server, kept between rebuilds.
 * A save replaces the entry: once the file's tab is closed, the outline reads
 * what was saved, not what was fetched before the file was opened.
 */
export class OutlineCache {
  private files = new Map<string, Promise<string | null>>()
  private fetch: (path: string) => Promise<string | null>
  constructor(fetch: (path: string) => Promise<string | null>) {
    this.fetch = fetch
  }

  read(path: string): Promise<string | null> {
    let f = this.files.get(path)
    if (!f) {
      f = this.fetch(path).catch(() => null)
      this.files.set(path, f)
    }
    return f
  }

  saved(path: string, content: string) {
    this.files.set(path, Promise.resolve(content))
  }

  clear() {
    this.files.clear()
  }
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
