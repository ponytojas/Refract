import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import {
  type Completion,
  CompletionContext,
  type CompletionResult,
  insertCompletionText,
  nextSnippetField,
} from '@codemirror/autocomplete'
import { EditorState, type Transaction, type TransactionSpec } from '@codemirror/state'
import { type EditorView, showTooltip } from '@codemirror/view'

import {
  type CompletionEnv,
  inLiteral,
  latexCompletion,
  needsEnd,
  noSymbols,
  opensArgument,
  finishedCommand,
  relativeTo,
  signatureAt,
  signatureHint,
} from './completion.ts'

const env: CompletionEnv = {
  symbols: {
    labels: ['fig:plot', 'sec:intro'],
    citations: [
      { key: 'knuth1984', title: 'Literate Programming', author: 'Knuth', year: '1984' },
      { key: 'lamport94' },
    ],
    files: ['main.tex', 'sec/intro.tex', 'sec/methods.TEX', 'fig/a.png', 'fig/b.PDF', 'refs.bib', 'notes.txt'],
  },
  path: 'main.tex',
  mainFile: 'main.tex',
}

// A document with | at the cursor.
function stateAt(marked: string) {
  const pos = marked.indexOf('|')
  return EditorState.create({ doc: marked.replace('|', ''), selection: { anchor: pos } })
}

function complete(marked: string, explicit = false, e = env) {
  const state = stateAt(marked)
  const context = new CompletionContext(state, state.selection.main.head, explicit)
  return latexCompletion(() => e)(context) as CompletionResult | null
}

const labels = (marked: string, e = env) => complete(marked, false, e)?.options.map((o) => o.label)

// Accept an option and return the document with | at the cursor.
function accept(marked: string, label: string, e = env, then?: (view: EditorView) => void) {
  const result = complete(marked, false, e)
  assert.ok(result, `no completions for ${marked}`)
  const option = result.options.find((o) => o.label === label) as Completion
  assert.ok(option, `no option ${label}`)
  const view = {
    state: stateAt(marked),
    dispatch(spec: Transaction | TransactionSpec) {
      view.state = 'startState' in spec ? spec.state : view.state.update(spec).state
    },
  }
  const to = view.state.selection.main.head
  if (typeof option.apply === 'function') {
    option.apply(view as unknown as EditorView, option, result.from, to)
  } else {
    view.dispatch(insertCompletionText(view.state, option.apply ?? option.label, result.from, to))
  }
  then?.(view as unknown as EditorView)
  const doc = view.state.doc.toString()
  const head = view.state.selection.main.head
  return `${doc.slice(0, head)}|${doc.slice(head)}`
}

test('commands complete after a backslash, not in prose or after a line break', () => {
  assert.ok(labels('\\sec|')?.includes('\\section'))
  assert.equal(complete('\\sec|')?.from, 0)
  assert.equal(complete('Plain prose|'), null)
  assert.equal(complete('Plain prose|', true), null)
  assert.equal(complete('\\\\foo|'), null)
  assert.equal(complete('\\\\ref{|}'), null)
  assert.ok(labels('\\\\\\sec|')?.includes('\\section'))
})

test('a finished command leaves Enter alone unless completion was asked for', () => {
  assert.equal(complete('\\item|'), null)
  assert.equal(complete('\\centering|'), null)
  assert.ok(complete('\\item|', true))
  const validFor = complete('\\ite|')?.validFor as (text: string) => boolean
  assert.equal(validFor('\\item'), false)
  assert.equal(validFor('\\ite'), true)
})

test('commands add their arguments, unless they are already there', () => {
  assert.equal(accept('\\sec|', '\\section'), '\\section{|}')
  assert.equal(accept('\\sec|{Intro}', '\\section'), '\\section|{Intro}')
  assert.equal(accept('\\ite|', '\\item'), '\\item|')
  assert.equal(accept('\\fra|', '\\frac'), '\\frac{|}{}')
})

test('Tab moves to the next argument of a command', () => {
  assert.equal(accept('\\fra|', '\\frac', env, nextSnippetField), '\\frac{}{|}')
  assert.equal(accept('\\hre|', '\\href', env, nextSnippetField), '\\href{}{|}')
})

test('commands with a list of their own reopen completion', () => {
  const option = (label: string) => complete('\\|')?.options.find((o) => o.label === label) as Completion
  for (const label of ['\\ref', '\\cite', '\\begin', '\\includegraphics'])
    assert.ok(opensArgument(option(label)), label)
  assert.equal(opensArgument(option('\\section')), false)
  assert.equal(accept('\\re|', '\\ref'), '\\ref{|}')
})

test('\\begin closes itself', () => {
  assert.equal(accept('\\begin{doc|}', 'document'), '\\begin{document}\n  |\n\\end{document}')
  assert.equal(accept('\\begin{doc|', 'document'), '\\begin{document}\n  |\n\\end{document}')
  assert.equal(accept('\\begin{ite|}', 'itemize'), '\\begin{itemize}\n  \\item |\n\\end{itemize}')
  assert.equal(accept('  \\begin{ite|}', 'itemize'), '  \\begin{itemize}\n    \\item |\n  \\end{itemize}')
  assert.equal(accept('\\begin{ite|m}', 'itemize'), '\\begin{itemize}\n  \\item |\n\\end{itemize}')
})

test('\\begin keeps a matching \\end and handles nesting', () => {
  assert.equal(
    accept('\\begin{ite|}\n\\item a\n\\end{itemize}', 'itemize'),
    '\\begin{itemize}|\n\\item a\n\\end{itemize}',
  )
  const nested = accept('\\begin{itemize}\n\\item \\begin{ite|}\n\\end{itemize}', 'itemize')
  assert.equal(nested.match(/\\end\{itemize\}/g)?.length, 2)
  const inside = accept('\\begin{itemize}\n\\begin{ite|}\n\\end{itemize}\n\\end{itemize}', 'itemize')
  assert.equal(inside.match(/\\end\{itemize\}/g)?.length, 2)
  const sibling = accept('\\begin{itemize}\n\\end{itemize}\n\\begin{ite|}', 'itemize')
  assert.equal(sibling.match(/\\end\{itemize\}/g)?.length, 2)
})

test('needsEnd ignores the environment being completed', () => {
  assert.equal(needsEnd('\\begin{a}', 'a', 7, 9), true)
  assert.equal(needsEnd('\\begin{a}\\end{a}', 'a', 7, 9), false)
  assert.equal(needsEnd('\\begin{a}\\begin{a}\\end{a}', 'a', 16, 18), true)
})

test('\\end completes a name without adding anything else', () => {
  assert.equal(accept('\\end{ite|}', 'itemize'), '\\end{itemize}|')
  assert.equal(accept('\\end{ite|', 'itemize'), '\\end{itemize}|')
})

test('references complete from labels', () => {
  assert.deepEqual(labels('\\ref{|}'), ['fig:plot', 'sec:intro'])
  assert.deepEqual(labels('\\eqref{|}'), ['fig:plot', 'sec:intro'])
  assert.deepEqual(labels('\\autoref{|}'), ['fig:plot', 'sec:intro'])
  assert.equal(accept('\\ref{se|}', 'sec:intro'), '\\ref{sec:intro}|')
  assert.equal(accept('\\ref{se|', 'sec:intro'), '\\ref{sec:intro}|')
  assert.equal(accept('\\ref{se|.', 'sec:intro'), '\\ref{sec:intro}|.')
  assert.equal(accept('\\ref{se|c:old} more', 'sec:intro'), '\\ref{sec:intro}| more')
  assert.equal(accept('See \\ref{|} and {x}', 'sec:intro'), 'See \\ref{sec:intro}| and {x}')
})

test('citations complete one key of a list', () => {
  assert.deepEqual(labels('\\cite{|}'), ['knuth1984', 'lamport94'])
  assert.deepEqual(labels('\\citep[p. 3]{|}'), ['knuth1984', 'lamport94'])
  assert.deepEqual(labels('\\parencite[see][12]{|}'), ['knuth1984', 'lamport94'])
  assert.equal(accept('\\cite{knu|}', 'knuth1984'), '\\cite{knuth1984|}')
  assert.equal(accept('\\cite{knu|', 'knuth1984'), '\\cite{knuth1984}|')
  assert.equal(accept('\\cite{knuth1984,la|}', 'lamport94'), '\\cite{knuth1984,lamport94|}')
  assert.equal(accept('\\cite{knuth1984, la|}', 'lamport94'), '\\cite{knuth1984, lamport94|}')
  assert.equal(accept('\\cite{kn|uth,lamport94}', 'knuth1984'), '\\cite{knuth1984|,lamport94}')
  assert.equal(complete('\\cite{knuth1984, la|}')?.from, 17)
})

test('input offers .tex files without their extension, and not the file being edited', () => {
  assert.deepEqual(labels('\\input{|}'), ['sec/intro', 'sec/methods'])
  assert.deepEqual(labels('\\include{|}'), ['sec/intro', 'sec/methods'])
  assert.deepEqual(labels('\\input{|}', { ...env, path: 'sec/intro.tex' }), ['main', 'sec/methods'])
  assert.equal(accept('\\input{sec/in|}', 'sec/intro'), '\\input{sec/intro}|')
})

test('includegraphics offers images with their extension', () => {
  assert.deepEqual(labels('\\includegraphics{|}'), ['fig/a.png', 'fig/b.PDF'])
  assert.deepEqual(labels('\\includegraphics[width=2cm]{|}'), ['fig/a.png', 'fig/b.PDF'])
  assert.equal(accept('\\includegraphics[width=2cm]{|}', 'fig/a.png'), '\\includegraphics[width=2cm]{fig/a.png}|')
})

test('paths with spaces are replaced whole', () => {
  const spaced = { ...env, symbols: { ...noSymbols, files: ['main.tex', 'fig/new image.png'] } }
  assert.equal(
    accept('\\includegraphics{fig/ne| image.png}', 'fig/new image.png', spaced),
    '\\includegraphics{fig/new image.png}|',
  )
  assert.equal(accept('\\includegraphics{ne|}', 'fig/new image.png', spaced), '\\includegraphics{fig/new image.png}|')
})

test('paths are relative to the main file, wherever the current file is', () => {
  const nested = { ...env, path: 'paper/sec/a.tex', mainFile: 'paper/main.tex' }
  const symbols = { ...noSymbols, files: ['paper/sec/a.tex', 'paper/sec/b.tex', 'assets/x.png', 'paper/fig/y.png'] }
  assert.deepEqual(labels('\\input{|}', { ...nested, symbols }), ['sec/b'])
  assert.deepEqual(labels('\\includegraphics{|}', { ...nested, symbols }), ['../assets/x.png', 'fig/y.png'])
  assert.deepEqual(labels('\\input{|}', { ...env, path: 'x.tex', mainFile: '' }), ['main', 'sec/intro', 'sec/methods'])
})

test('relativeTo walks up only as far as needed', () => {
  assert.equal(relativeTo('', 'fig/a.png'), 'fig/a.png')
  assert.equal(relativeTo('paper', 'paper/fig/a.png'), 'fig/a.png')
  assert.equal(relativeTo('paper', 'assets/a.png'), '../assets/a.png')
  assert.equal(relativeTo('a/b', 'a/c/d.png'), '../c/d.png')
  assert.equal(relativeTo('a/b', 'top.png'), '../../top.png')
  assert.equal(relativeTo('a', 'a'), '../a')
})

test('nothing completes in comments or literal environments', () => {
  assert.equal(complete('% \\ref{|}'), null)
  assert.equal(complete('text % \\sec|'), null)
  assert.ok(complete('100\\% \\ref{|}'))
  assert.equal(complete('\\begin{verbatim}\n\\ref{|}'), null)
  assert.equal(complete('\\begin{lstlisting}\n\\sec|'), null)
  assert.ok(complete('\\begin{verbatim}\nx\n\\end{verbatim}\n\\ref{|}'))
  assert.ok(complete('% \\begin{verbatim}\n\\ref{|}'))
  assert.equal(complete('\\begin{verbatim}\n50% \\end{verbatim}\n% \\ref{|}'), null)
  assert.ok(complete('\\newcommand{\\figref}[1]{Figure~\\ref{|}}'))
})

test('inLiteral stops being a comment at the end of the line', () => {
  assert.equal(inLiteral('% comment'), true)
  assert.equal(inLiteral('% comment\n'), false)
  assert.equal(inLiteral('50\\% '), false)
  assert.equal(inLiteral('\\begin{comment}'), true)
})

test('arguments of other commands get no completion', () => {
  assert.equal(complete('\\section{Intro|}'), null)
  assert.equal(complete('\\begin{tabular}{l|}'), null)
})

test('commands and environments carry their help, and project symbols do not', () => {
  const help = { '\\frac': { detail: '{a}{b}', info: 'Fraction' }, figure: { info: 'Floating figure' } }
  const context = (marked: string) => {
    const state = stateAt(marked)
    return new CompletionContext(state, state.selection.main.head, false)
  }
  const options = (marked: string, e = env) =>
    (latexCompletion(() => e, help)(context(marked)) as CompletionResult).options
  const frac = options('\\fra|').find((o) => o.label === '\\frac')
  assert.equal(frac?.detail, '{a}{b}')
  assert.equal(frac?.info, 'Fraction')
  assert.equal(options('\\begin{|}').find((o) => o.label === 'figure')?.info, 'Floating figure')
  assert.equal(options('\\end{|}').find((o) => o.label === 'figure')?.info, 'Floating figure')
  const symbols = { ...noSymbols, labels: ['figure'] }
  assert.equal(options('\\ref{|}', { ...env, symbols })[0].info, undefined)
})

test('everything offered has an entry in the help table', () => {
  const table = readFileSync(new URL('./completion-help.ts', import.meta.url), 'utf8')
  const offered = [...(labels('\\|') ?? []), ...(labels('\\begin{|}') ?? [])]
  assert.ok(offered.length > 50)
  const key = (label: string) => `'${label.replaceAll('\\', '\\\\')}':`
  const missing = offered.filter((l) => !table.includes(key(l)) && !table.includes(`  ${l}:`))
  assert.deepEqual(missing, [])
})

test('citations say which work a key is', () => {
  const [knuth, lamport] = complete('\\cite{|}')?.options ?? []
  assert.equal(knuth.detail, 'Knuth, 1984')
  assert.equal(knuth.info, 'Literate Programming')
  assert.equal(lamport.detail, undefined)
  assert.equal(lamport.info, undefined)
})

const help = {
  '\\frac': { detail: '{num}{den}', info: 'Fraction' },
  '\\includegraphics': { detail: '[options]{file}' },
  '\\footnote': { detail: '{text}' },
  '\\alpha': { detail: 'α' },
}
const active = (before: string) => {
  const s = signatureAt(before, help)
  return s && s.parts[s.active]
}

test('the signature follows the argument being filled in', () => {
  assert.equal(active('\\frac{'), '{num}')
  assert.equal(active('\\frac{a'), '{num}')
  assert.equal(active('\\frac{a}{'), '{den}')
  assert.equal(active('\\frac{a}{b'), '{den}')
  assert.equal(active('$x = \\frac{a}{b'), '{den}')
  assert.equal(signatureAt('$x = \\frac{a', help)?.from, 5)
  assert.equal(active('\\includegraphics['), '[options]')
})

test('the signature sees through nested groups', () => {
  assert.equal(active('\\frac{x^{2'), '{num}')
  assert.equal(active('\\frac{x^{2}'), '{num}')
  assert.equal(active('\\frac{a}{\\textbf{b'), '{den}')
  assert.equal(active('\\frac{a[1]}{'), '{den}')
})

test('a lone argument, a finished command or an escaped brace show no signature', () => {
  assert.equal(signatureAt('\\footnote{some prose', help), null)
  assert.equal(signatureAt('\\includegraphics{a', help), null)
  assert.equal(signatureAt('\\frac{a}{b} and', help), null)
  assert.equal(signatureAt('\\frac\\{a', help), null)
  assert.equal(signatureAt('\\\\frac{a', help), null)
  assert.equal(signatureAt('\\alpha{a', help), null)
  assert.equal(signatureAt('\\frac{a}{b}{c', help), null)
  assert.equal(signatureAt('plain text', help), null)
})

test('the hint appears as a tooltip, except in comments', () => {
  const tooltips = (marked: string) => {
    const pos = marked.indexOf('|')
    const state = EditorState.create({
      doc: marked.replace('|', ''),
      selection: { anchor: pos },
      extensions: signatureHint(help),
    })
    return state.facet(showTooltip).filter(Boolean)
  }
  assert.equal(tooltips('\\frac{|}{}').length, 1)
  assert.equal(tooltips('% \\frac{|}{}').length, 0)
  assert.equal(tooltips('text|').length, 0)
})

test('a finished command keeps its help once its list has closed', () => {
  const frac = finishedCommand('$x = \\frac', help)
  assert.deepEqual(frac, { from: 5, label: '\\frac', parts: ['{num}', '{den}'], active: -1, info: 'Fraction' })
  assert.deepEqual(finishedCommand('\\alpha', help)?.parts, [' α'])
  assert.equal(finishedCommand('\\fra', help), null)
  assert.equal(finishedCommand('\\\\frac', help), null)
  assert.equal(finishedCommand('\\frac{', help), null)
})

test('the help of a finished command shows while typing only', () => {
  const start = EditorState.create({ doc: '\\fra', selection: { anchor: 4 }, extensions: signatureHint(help) })
  const shown = (s: EditorState) => s.facet(showTooltip).filter(Boolean).length
  const typed = start.update({
    changes: { from: 4, insert: 'c' },
    selection: { anchor: 5 },
    userEvent: 'input.type',
  }).state
  assert.equal(shown(typed), 1)
  assert.equal(shown(typed.update({ selection: { anchor: 0 } }).state), 0)
  assert.equal(shown(typed.update({ selection: { anchor: 5 } }).state), 0) // moved back onto it
  const space = typed.update({ changes: { from: 5, insert: ' ' }, selection: { anchor: 6 }, userEvent: 'input.type' })
  assert.equal(shown(space.state), 0)
  const accepted = start.update({
    changes: { from: 4, insert: 'c' },
    selection: { anchor: 5 },
    userEvent: 'input.complete',
  }).state
  assert.equal(shown(accepted), 0)
})

test('the hint changes with the command, even at the same position', () => {
  const both = { ...help, '\\fracs': { detail: '{a}{b}' } }
  const start = EditorState.create({ doc: '\\frac', selection: { anchor: 5 }, extensions: signatureHint(both) })
  const typed = start.update({
    changes: { from: 4, to: 5, insert: 'c' },
    selection: { anchor: 5 },
    userEvent: 'input.type',
  }).state
  const next = typed.update({
    changes: { from: 5, insert: 's' },
    selection: { anchor: 6 },
    userEvent: 'input.type',
  }).state
  assert.notEqual(next.facet(showTooltip).find(Boolean), typed.facet(showTooltip).find(Boolean))
})
