import {
  type Completion,
  type CompletionContext,
  type CompletionResult,
  type CompletionSource,
  insertCompletionText,
  pickedCompletion,
  snippet,
} from '@codemirror/autocomplete'
import { type EditorState, type Extension, StateField } from '@codemirror/state'
import { type EditorView, showTooltip, type Tooltip } from '@codemirror/view'

export type Citation = { key: string; title?: string; author?: string; year?: string }
export type ProjectSymbols = { labels: string[]; citations: Citation[]; files: string[] }
export const noSymbols: ProjectSymbols = { labels: [], citations: [], files: [] }
export type CompletionEnv = { symbols: ProjectSymbols; path: string; mainFile: string }
// By label: detail shows next to the name (its arguments), info in a panel beside the list.
export type CompletionHelp = Record<string, { detail?: string; info?: string }>
type Lists = { commands: Completion[]; begin: Completion[]; end: Completion[] }

type Apply = (view: EditorView, completion: Completion, from: number, to: number) => void

const words = (s: string) => s.split(' ')

const refCommands = words('ref eqref autoref pageref cref Cref nameref')
const citeCommands = words('cite citep citet parencite textcite autocite footcite nocite')
const fileCommands = words('input include includegraphics')
// Commands whose argument has a list of its own: accepting one opens that list.
const openers = new Set([...refCommands, ...citeCommands, ...fileCommands, 'begin', 'end'].map((name) => `\\${name}`))

const noArgument = words(
  'item centering maketitle tableofcontents listoffigures listoftables appendix newpage clearpage noindent ' +
    'hline toprule midrule bottomrule bigskip medskip smallskip hfill vfill ldots cdots dots ' +
    'alpha beta gamma delta epsilon theta lambda mu pi sigma phi omega infty sum prod int partial nabla ' +
    'times cdot leq geq neq approx rightarrow leftarrow Rightarrow',
)
const oneArgument = words(
  'part chapter section subsection subsubsection paragraph caption label usepackage documentclass ' +
    'bibliography addbibresource bibliographystyle title author date ' +
    'textbf textit texttt textsc emph underline footnote url sqrt mathbf mathrm mathcal mathbb hat bar vec',
)
const twoArguments = words('frac href')

const environments = words(
  'abstract align align* array center description document enumerate equation equation* figure figure* ' +
    'flushleft flushright frame gather gather* itemize lstlisting minipage quote table table* tabular theorem ' +
    'proof verbatim',
)
const listEnvironments = new Set(['description', 'enumerate', 'itemize'])

// A backslash starts a command only after an even number of backslashes: \\foo is a line break and "foo".
const commandBefore = /(?:^|[^\\])(?:\\\\)*(\\[a-zA-Z]*)$/
const argumentBefore = /(?:^|[^\\])(?:\\\\)*\\([a-zA-Z]+)\*?(?:\[[^\]]*\]){0,2}\{([^{}]*)$/
const literalToken = /%[^\n]*|\\begin\{(verbatim\*?|Verbatim\*?|lstlisting|minted|comment)\}|\\[^]/g

// closeBrackets adds the closing brace only before spaces and closing punctuation, so it may be missing.
const closedArgument = /^[^\s{}\\]*\}/
// File names may contain spaces, so a path argument runs to its closing brace.
const closedPath = /^[^{}\\]*\}/
const citeTail = /^[^\s{},\\]*(?=[,}])/

function tailEnd(state: EditorState, to: number, tail: RegExp): number | null {
  const line = state.doc.lineAt(to)
  const m = line.text.slice(to - line.from).match(tail)
  return m ? to + m[0].length : null
}

function insert(view: EditorView, completion: Completion, text: string, from: number, to: number) {
  view.dispatch({
    ...insertCompletionText(view.state, text, from, to),
    annotations: pickedCompletion.of(completion),
  })
}

// Insert a whole argument and leave the cursor after its closing brace.
const applyArgument =
  (text: string, tail: RegExp = closedArgument): Apply =>
  (view, completion, from, to) => {
    const end = tailEnd(view.state, to, tail)
    insert(view, completion, `${text}}`, from, end ?? to)
  }

// Replace one key of a list, leaving the cursor in the list so a comma can add another.
const applyCiteKey =
  (key: string): Apply =>
  (view, completion, from, to) => {
    const end = tailEnd(view.state, to, citeTail)
    if (end === null) insert(view, completion, `${key}}`, from, to)
    else insert(view, completion, key, from, end)
  }

const applyBegin =
  (name: string): Apply =>
  (view, completion, from, to) => {
    const end = tailEnd(view.state, to, closedArgument) ?? to
    if (!needsEnd(view.state.doc.toString(), name, from, end)) {
      insert(view, completion, `${name}}`, from, end)
      return
    }
    const body = listEnvironments.has(name) ? '\\item ' : ''
    snippet(`${name}}\n\t${body}\${}\n\\end{${name}}`)(view, completion, from, end)
  }

// Add empty arguments, unless the command being completed already has them.
const applyCommand =
  (label: string, args: number): Apply =>
  (view, completion, from, to) => {
    const next = view.state.sliceDoc(to, to + 1)
    if (next === '{' || next === '[') insert(view, completion, label, from, to)
    // Braces unescaped on purpose: snippet() misplaces a field that follows several escapes.
    else snippet(label + '{${}}'.repeat(args))(view, completion, from, to)
  }

const commands: Completion[] = [
  ...noArgument.map((name) => ({ label: `\\${name}` })),
  ...[...oneArgument, ...openers].map((name) => {
    const label = name.startsWith('\\') ? name : `\\${name}`
    return { label, apply: applyCommand(label, 1) }
  }),
  ...twoArguments.map((name) => ({ label: `\\${name}`, apply: applyCommand(`\\${name}`, 2) })),
]
const known = new Set(commands.map((c) => c.label))

function commandResult(before: string, pos: number, explicit: boolean, lists: Lists): CompletionResult | null {
  const word = before.match(commandBefore)?.[1]
  if (word === undefined) return null
  // A finished command at the end of a line: Enter must break the line, not accept it.
  if (known.has(word) && !explicit) return null
  return {
    from: pos - word.length,
    options: lists.commands,
    validFor: (text) => /^\\[a-zA-Z]*$/.test(text) && !known.has(text),
  }
}

function argumentOptions(name: string, env: CompletionEnv, lists: Lists): CompletionResult['options'] | null {
  if (name === 'begin') return lists.begin
  if (name === 'end') return lists.end
  if (refCommands.includes(name)) {
    return env.symbols.labels.map((label) => ({ label, apply: applyArgument(label) }))
  }
  if (/cite/i.test(name)) {
    return env.symbols.citations.map(({ key, title, author, year }) => ({
      label: key,
      detail: [author, year].filter(Boolean).join(', ') || undefined,
      info: title,
      apply: applyCiteKey(key),
    }))
  }
  if (!fileCommands.includes(name)) return null
  // TeX resolves these from the directory Tectonic runs in: the main file's.
  const dir = env.mainFile.includes('/') ? env.mainFile.slice(0, env.mainFile.lastIndexOf('/')) : ''
  const graphics = name === 'includegraphics'
  const files = env.symbols.files.filter((f) =>
    graphics ? /\.(?:pdf|png|jpe?g)$/i.test(f) : /\.tex$/i.test(f) && f !== env.path,
  )
  return files.map((f) => {
    const relative = relativeTo(dir, f)
    // \include{a.tex} would look for a.tex.tex.
    const label = graphics ? relative : relative.replace(/\.tex$/i, '')
    return { label, apply: applyArgument(label, closedPath) }
  })
}

function argumentResult(before: string, pos: number, env: CompletionEnv, lists: Lists): CompletionResult | null {
  const m = before.match(argumentBefore)
  if (!m) return null
  const [, name, argument] = m
  const options = argumentOptions(name, env, lists)
  if (!options) return null
  if (/cite/i.test(name)) {
    const key = argument.match(/[^,\s]*$/)?.[0] ?? ''
    return { from: pos - key.length, options, validFor: /^[^\s{},\\]*$/ }
  }
  const path = fileCommands.includes(name)
  return { from: pos - argument.length, options, validFor: path ? /^[^{},\\]*$/ : /^[^\s{},\\]*$/ }
}

export function latexCompletion(env: () => CompletionEnv, help: CompletionHelp = {}): CompletionSource {
  const explained = (c: Completion) => ({ ...c, ...help[c.label] })
  const lists = {
    commands: commands.map(explained),
    begin: environments.map((label) => explained({ label, apply: applyBegin(label) })),
    end: environments.map((label) => explained({ label, apply: applyArgument(label) })),
  }
  return (context: CompletionContext) => {
    const { state, pos, explicit } = context
    const line = state.doc.lineAt(pos)
    const before = line.text.slice(0, pos - line.from)
    const result = commandResult(before, pos, explicit, lists) ?? argumentResult(before, pos, env(), lists)
    // Checked last: it reads the document up to the cursor, and most keystrokes end above.
    return result && !inLiteral(state.sliceDoc(0, pos)) ? result : null
  }
}

type Signature = { from: number; label: string; parts: string[]; active: number; info?: string }

const argumentParts = /\[[^\]]*\]|\{[^}]*\}/g

const isEscaped = (text: string, at: number) => {
  let i = at
  while (i > 0 && text[i - 1] === '\\') i--
  return (at - i) % 2 === 1
}

// The index of the { or [ left open before end, or -1.
function openBefore(text: string, end: number): number {
  let depth = 0
  for (let i = end - 1; i >= 0; i--) {
    if (isEscaped(text, i)) continue
    if (text[i] === '}' || text[i] === ']') depth++
    else if (text[i] === '{' || text[i] === '[') {
      if (depth === 0) return i
      depth--
    }
  }
  return -1
}

// The command an open argument belongs to, and the brackets of its arguments so far.
function commandOf(before: string, open: number) {
  const brackets = [before[open]]
  let end = open
  while (before[end - 1] === '}' || before[end - 1] === ']') {
    const start = openBefore(before, end - 1)
    if (start < 0) return null
    brackets.unshift(before[start])
    end = start
  }
  const label = before.slice(0, end).match(/(?:^|[^\\])(?:\\\\)*(\\[a-zA-Z]+)\*?$/)?.[1]
  return label ? { label, from: before.lastIndexOf(label, end), brackets } : null
}

// Which argument of a detail such as [a]{b}{c} the written brackets reach: optional ones may be left out.
function activePart(parts: string[], brackets: string[]): number {
  let at = -1
  for (const bracket of brackets) {
    at = parts.findIndex((part, i) => i > at && part[0] === bracket)
    if (at < 0) return -1
  }
  return at
}

// The signature to keep in view at the end of before, looking outwards from the
// innermost argument. An only argument isn't worth covering the text around it:
// \footnote{…} is prose, and its completions, if any, already explain it.
export function signatureAt(before: string, help: CompletionHelp): Signature | null {
  for (let open = openBefore(before, before.length); open >= 0; open = openBefore(before, open)) {
    const command = commandOf(before, open)
    const entry = command && help[command.label]
    const parts = entry?.detail?.match(argumentParts)
    if (!command || !parts) continue
    const active = activePart(parts, command.brackets)
    if (active < 0) continue
    const required = parts.filter((part) => part[0] === '{').length
    if (required < 2 && parts[active][0] !== '[') continue
    return { from: command.from, label: command.label, parts, active, info: entry?.info }
  }
  return null
}

// The help of a command just finished at the end of before. Its list has closed by
// then, so Enter can break the line, and this keeps what the list was showing.
export function finishedCommand(before: string, help: CompletionHelp): Signature | null {
  const label = before.match(commandBefore)?.[1]
  const entry = label ? help[label] : undefined
  if (!label || !entry) return null
  const parts = entry.detail?.match(argumentParts) ?? (entry.detail ? [` ${entry.detail}`] : [])
  return { from: before.length - label.length, label, parts, active: -1, info: entry.info }
}

function hintDOM({ label, parts, active, info }: Signature) {
  const dom = document.createElement('div')
  dom.className = 'cm-signatureHint'
  const code = dom.appendChild(document.createElement('code'))
  code.append(label)
  parts.forEach((part, i) => {
    const span = code.appendChild(document.createElement('span'))
    span.textContent = part
    if (i === active) span.className = 'cm-signatureHint-active'
  })
  if (info) dom.appendChild(document.createElement('p')).textContent = info
  return { dom }
}

// Keeps a command's arguments in view while they are being filled in, as the list did while choosing it.
export function signatureHint(help: CompletionHelp): Extension {
  type Hint = Tooltip & { signature: Signature }
  const compute = (state: EditorState, previous: Hint | null, typed: boolean): Hint | null => {
    const head = state.selection.main.head
    const line = state.doc.lineAt(head)
    const before = line.text.slice(0, head - line.from)
    // Only while typing: moving the cursor along written commands shouldn't pop anything up.
    const signature = (typed ? finishedCommand(before, help) : null) ?? signatureAt(before, help)
    if (!signature || inLiteral(state.sliceDoc(0, head))) return null
    const pos = line.from + signature.from
    // Rebuild only when it changes, or the tooltip would be redrawn on every keystroke.
    const same = previous?.signature.label === signature.label && previous.signature.active === signature.active
    if (same && previous?.pos === pos) return previous
    return { pos, above: true, signature, create: () => hintDOM(signature) }
  }
  return StateField.define<Hint | null>({
    create: (state) => compute(state, null, false),
    update: (hint, tr) =>
      tr.docChanged || tr.selection ? compute(tr.state, hint, tr.isUserEvent('input.type')) : hint,
    provide: (field) => showTooltip.from(field),
  })
}

// Accepting one of these leaves the cursor in an argument with completions of its own.
export function opensArgument(completion: Completion): boolean {
  return openers.has(completion.label)
}

// Whether text ending at the cursor leaves it in a comment or a literal environment.
// One left-to-right pass, because each can hide the other's markers.
export function inLiteral(before: string): boolean {
  literalToken.lastIndex = 0
  for (let m; (m = literalToken.exec(before));) {
    if (m[0][0] === '%') {
      if (literalToken.lastIndex === before.length) return true
    } else if (m[1]) {
      const close = `\\end{${m[1]}}`
      const at = before.indexOf(close, literalToken.lastIndex)
      if (at < 0) return true
      literalToken.lastIndex = at + close.length
    }
  }
  return false
}

// Whether a \begin{name} at from..to lacks its \end: an \end left over once the other
// pairs are matched belongs to it, as when renaming an existing environment.
export function needsEnd(doc: string, name: string, from: number, to: number): boolean {
  const rest = doc.slice(0, from) + doc.slice(to)
  const count = (s: string) => rest.split(s).length - 1
  return count(`\\end{${name}}`) <= count(`\\begin{${name}}`)
}

// The path of file, given from the root, as seen from dir ('' is the root).
export function relativeTo(dir: string, file: string): string {
  const from = dir ? dir.split('/') : []
  const to = file.split('/')
  let shared = 0
  while (shared < from.length && shared < to.length - 1 && from[shared] === to[shared]) shared++
  return [...from.slice(shared).map(() => '..'), ...to.slice(shared)].join('/')
}
