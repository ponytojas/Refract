// Visible prose with source offsets: selections are filtered AFTER parsing the
// whole file, so selecting inside a comment, command or equation stays excluded.
export type CountRules = {
  titles: boolean
  footnotes: boolean
  captions: boolean
  tables: boolean
  bibliography: boolean
  citations: boolean
}
export const defaultRules: CountRules = {
  titles: true,
  footnotes: true,
  captions: true,
  tables: true,
  bibliography: false,
  citations: false,
}
export type Word = { text: string; from: number; to: number }
export type Section = { id: string; name: string; from: number; to: number; headingFrom: number }
type Char = { char: string; from: number; to: number }

const headings = /^(?:part|chapter|section|subsection|subsubsection|paragraph|subparagraph|title)$/
const silent =
  /^(?:documentclass|usepackage|RequirePackage|label|ref|eqref|pageref|autoref|cref|Cref|bibliography|bibliographystyle|addbibresource|includegraphics|input|include|includeonly|index|glossary|vspace|hspace|setlength|addtolength|setcounter|addtocounter|color|pagecolor|pagestyle|thispagestyle|pagenumbering|hyphenation|ensuremath)$/
const mathEnvs = /^(?:equation|align|alignat|gather|multline|flalign|displaymath|math|eqnarray)\*?$/
const tableEnvs = /^(?:tabular|tabularx|longtable|array)\*?$/

export function plainWords(text: string): Word[] {
  // Join PDF line-wrap hyphenation, normalize ligatures, preserve ordinary
  // hyphens/apostrophes inside words. Punctuation alone is never a word.
  const chars: Char[] = []
  for (let i = 0; i < text.length;) {
    const split = text.slice(i).match(/^-\s*\r?\n\s*(?=\p{L})/u)
    if (split && i > 0 && /\p{L}/u.test(text[i - 1])) {
      i += split[0].length
      continue
    }
    const cp = String.fromCodePoint(text.codePointAt(i)!)
    for (const char of cp.normalize('NFKC')) chars.push({ char, from: i, to: i + cp.length })
    i += cp.length
  }
  return wordsFrom(chars)
}

function wordsFrom(chars: Char[]): Word[] {
  const text = chars.map((c) => c.char).join('')
  // UTF-16 mapping also covers non-BMP scripts.
  const offsets = chars.flatMap((c) => Array.from({ length: c.char.length }, () => c))
  return [...text.matchAll(/[\p{L}\p{N}][\p{L}\p{M}\p{N}]*(?:[’'-][\p{L}\p{N}][\p{L}\p{M}\p{N}]*)*/gu)].map((m) => ({
    text: m[0],
    from: offsets[m.index].from,
    to: offsets[m.index + m[0].length - 1].to,
  }))
}

export function latexWords(source: string, rules: CountRules = defaultRules): Word[] {
  const out: Char[] = []
  let i = 0
  const emit = (char: string, from = i, to = i + 1) => {
    for (const c of char) out.push({ char: c, from, to })
  }
  const space = () => emit(' ')
  const ws = () => {
    while (/\s/.test(source[i] ?? '') && i < source.length) i++
  }
  const skipGroup = (open = '{', close = '}') => {
    if (source[i] !== open) return
    let depth = 1
    i++
    while (i < source.length && depth) {
      if (source[i] === '\\') {
        i += 2
        continue
      }
      if (source[i] === '%') {
        while (i < source.length && source[i] !== '\n') i++
        continue
      }
      if (source[i] === open) depth++
      if (source[i] === close) depth--
      i++
    }
  }
  const options = () => {
    ws()
    while (source[i] === '[') {
      skipGroup('[', ']')
      ws()
    }
  }
  const arg = () => {
    options()
    skipGroup()
  }
  const skipEnv = (name: string) => {
    const end = source.indexOf(`\\end{${name}}`, i)
    i = end < 0 ? source.length : end + name.length + 6
    space()
  }
  while (i < source.length) {
    const start = i
    const c = source[i]
    if (c === '%') {
      while (i < source.length && source[i] !== '\n') i++
      continue
    }
    if (c === '$') {
      const delimiter = source[i + 1] === '$' ? '$$' : '$'
      i += delimiter.length
      while (i < source.length) {
        if (source[i] === '\\') {
          i += 2
          continue
        }
        if (source.startsWith(delimiter, i)) {
          i += delimiter.length
          break
        }
        i++
      }
      space()
      continue
    }
    if (c !== '\\') {
      if (c !== '{' && c !== '}') emit(c === '~' || c === '&' ? ' ' : c)
      i++
      continue
    }
    i++
    if (source[i] === '(' || source[i] === '[') {
      const end = source.indexOf(source[i] === '(' ? '\\)' : '\\]', i + 1)
      i = end < 0 ? source.length : end + 2
      space()
      continue
    }
    const match = source.slice(i).match(/^[a-zA-Z@]+/)
    if (!match) {
      const symbol = source[i++] ?? ''
      if ('%&#_$'.includes(symbol)) emit(symbol, start, i)
      else if (symbol === '\\' || symbol === ' ' || symbol === ',' || symbol === ';') {
        space()
        if (symbol === '\\') options()
      }
      // Accent commands retain their argument without introducing a boundary.
      continue
    }
    let command = match[0]
    i += command.length
    if (source[i] === '*') i++
    if (command === 'captionof') {
      arg()
      command = 'caption'
    }
    if (command === 'footnotetext' || command === 'thanks') command = 'footnote'
    if (
      /^(?:par|newline|linebreak|pagebreak|newpage|clearpage|cleardoublepage|noindent|medskip|smallskip|bigskip|quad|qquad)$/.test(
        command,
      )
    ) {
      options()
      space()
      continue
    }
    if (command === 'begin' || command === 'end') {
      ws()
      const env = source.slice(i).match(/^\{([^}]+)\}/)
      if (!env) continue
      i += env[0].length
      if (command === 'end') {
        space()
        continue
      }
      const name = env[1]
      if (
        mathEnvs.test(name) ||
        name === 'comment' ||
        (!rules.bibliography && name === 'thebibliography') ||
        (!rules.tables && tableEnvs.test(name))
      ) {
        skipEnv(name)
        continue
      }
      if (name === 'thebibliography' || /^(?:tabular|tabularx|longtable|array)\*?$/.test(name)) {
        options()
        skipGroup()
        if (name === 'tabularx') arg()
      }
      space()
      continue
    }
    if (/^(?:newcommand|renewcommand|providecommand|DeclareRobustCommand)$/.test(command)) {
      arg()
      options()
      skipGroup()
      space()
      continue
    }
    if (/^(?:def|gdef|edef|xdef)$/.test(command)) {
      while (i < source.length && source[i] !== '{') i++
      skipGroup()
      space()
      continue
    }
    if (command === 'verb') {
      const delimiter = source[i++]
      while (i < source.length && source[i] !== delimiter) {
        emit(source[i])
        i++
      }
      i++
      continue
    }
    if (/^(?:cite\w*|[pt]cite\w*|nocite|bibitem)$/.test(command)) {
      if (rules.citations && command !== 'nocite' && command !== 'bibitem') {
        options()
        continue
      }
      arg()
      space()
      continue
    }
    if (silent.test(command)) {
      arg()
      space()
      continue
    }
    if (command === 'href' || command === 'textcolor' || command === 'colorbox' || command === 'foreignlanguage') {
      arg()
      options()
      continue
    }
    if (command === 'texorpdfstring' || headings.test(command) || command === 'footnote' || command === 'caption') {
      const enabled =
        command === 'texorpdfstring' ||
        (headings.test(command) ? rules.titles : command === 'footnote' ? rules.footnotes : rules.captions)
      options()
      const from = i
      skipGroup()
      const end = i
      if (enabled && end > from) {
        space()
        for (const word of latexWords(source.slice(from + 1, end - 1), rules)) {
          for (const char of word.text) emit(char, from + 1 + word.from, from + 1 + word.to)
          space()
        }
      }
      space()
      if (command === 'texorpdfstring') arg()
      continue
    }
    if (command === 'item') {
      options()
      space()
      continue
    }
    const symbols: Record<string, string> = {
      LaTeX: 'LaTeX',
      TeX: 'TeX',
      ae: 'æ',
      AE: 'Æ',
      oe: 'œ',
      OE: 'Œ',
      ss: 'ß',
      o: 'ø',
      O: 'Ø',
      l: 'ł',
      L: 'Ł',
      textendash: '–',
      textemdash: '—',
    }
    if (command in symbols) {
      emit(symbols[command], start, i)
      continue
    }
    // Formatting commands keep the mandatory content, discard optional style.
    options()
  }
  return wordsFrom(out)
}

export function selectedWords(words: Word[], from: number, to: number) {
  return words.filter((w) => w.to > from && w.from < to)
}

// Mask literal examples and definitions without moving any source positions.
function sectionSource(source: string): string {
  const chars = source.split('')
  const tokens = /%[^\n]*|\\(?:[a-zA-Z@]+\*?|[^])/g
  const skipGroup = (start: number, open = '{', close = '}') => {
    let i = start
    while (/\s/.test(source[i] ?? '') && i < source.length) i++
    if (source[i] !== open) return i
    let depth = 1
    for (i++; i < source.length && depth; i++) {
      if (source[i] === '\\') i++
      else if (source[i] === '%') {
        while (i < source.length && source[i] !== '\n') i++
      } else if (source[i] === open) depth++
      else if (source[i] === close) depth--
    }
    return i
  }
  for (let m; (m = tokens.exec(source));) {
    let end = tokens.lastIndex
    const command = m[0].replace(/\*$/, '')
    if (command === '\\begin') {
      const env = source.slice(end).match(/^\s*\{(verbatim\*?|Verbatim\*?|lstlisting|minted|comment)\}/)
      if (!env) continue
      const close = `\\end{${env[1]}}`
      const at = source.indexOf(close, end + env[0].length)
      end = at < 0 ? source.length : at + close.length
    } else if (command === '\\verb') {
      const at = source.indexOf(source[end], end + 1)
      end = at < 0 ? source.length : at + 1
    } else if (/^\\(?:newcommand|renewcommand|providecommand|DeclareRobustCommand)$/.test(command)) {
      end = skipGroup(end)
      // Also accept the unbraced form: \newcommand\example[1]{...}.
      const name = source.slice(end).match(/^\\[a-zA-Z@]+/)
      if (name) end += name[0].length
      end = skipGroup(end, '[', ']')
      end = skipGroup(end, '[', ']')
      end = skipGroup(end)
    } else if (/^\\(?:def|gdef|edef|xdef)$/.test(command)) {
      const at = source.indexOf('{', end)
      end = at < 0 ? source.length : skipGroup(at)
    } else if (!command.startsWith('%')) continue
    chars.fill(' ', m.index, end)
    tokens.lastIndex = end
  }
  return chars.join('')
}

export function findSections(source: string): Section[] {
  const clean = sectionSource(source)
  const result: Section[] = []
  let abstractIndex = 0
  for (const m of clean.matchAll(/\\begin\{abstract\}([\s\S]*?)\\end\{abstract\}/g)) {
    result.push({
      id: `abstract:${abstractIndex++}`,
      name: 'Abstract',
      headingFrom: m.index,
      from: m.index + '\\begin{abstract}'.length,
      to: m.index + m[0].lastIndexOf('\\end'),
    })
  }
  const sections = [
    ...clean.matchAll(
      /\\(chapter|section|subsection|subsubsection)\*?(?:\[[^\]]*\])?\{([^{}]*(?:\{[^{}]*\}[^{}]*)*)\}/g,
    ),
  ]
  const rank: Record<string, number> = { chapter: 0, section: 1, subsection: 2, subsubsection: 3 }
  const seen = new Map<string, number>()
  for (let n = 0; n < sections.length; n++) {
    const m = sections[n]
    const name =
      latexWords(m[2])
        .map((w) => w.text)
        .join(' ') || m[2]
    const key = `${m[1]}:${name}`
    const index = seen.get(key) ?? 0
    seen.set(key, index + 1)
    const next = sections.slice(n + 1).find((s) => rank[s[1]] <= rank[m[1]])
    const end = clean.indexOf('\\end{document}', m.index)
    result.push({
      id: `${key}:${index}`,
      name,
      headingFrom: m.index,
      from: m.index + m[0].length,
      to: next?.index ?? (end < 0 ? source.length : end),
    })
  }
  return result.sort((a, b) => a.from - b.from)
}

// Include the heading itself and prefer the innermost containing subsection.
export function sectionAt(source: string, pos: number): Section | undefined {
  return findSections(source)
    .filter((s) => pos >= s.headingFrom && pos < s.to)
    .sort((a, b) => b.headingFrom - a.headingFrom)[0]
}
