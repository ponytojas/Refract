// Run with: node --test web/src/lib/outline.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildOutline, currentHeading, depths, headingsIn, OutlineCache, resolveInclude } from './outline.ts'
import { defaultRules } from './wordcount.ts'

const reader = (files: Record<string, string>) => async (p: string) => files[p] ?? null
const titles = (o: { headings: { title: string }[] }) => o.headings.map((h) => h.title)

test('levels, starred and short-title headings, formatting in titles', () => {
  const src = [
    '\\part{One}',
    '\\chapter{Intro}',
    '\\section*{The \\emph{best} way}',
    '\\subsection[Short]{Long title}',
    '\\subsubsection{Deep}',
    '\\paragraph{Para}',
  ].join('\n')
  assert.deepEqual(
    headingsIn(src, 'main.tex').map((h) => [h.level, h.title, h.line]),
    [
      [0, 'One', 1],
      [1, 'Intro', 2],
      [2, 'The best way', 3],
      [3, 'Long title', 4],
      [4, 'Deep', 5],
      [5, 'Para', 6],
    ],
  )
})

test('ignores commented-out, verbatim and macro-definition headings', () => {
  const src = [
    '% \\section{Commented}',
    '\\begin{verbatim}',
    '\\section{Verbatim}',
    '\\end{verbatim}',
    '\\newcommand{\\mysec}{\\section{Macro}}',
    '50\\% \\section{Real}',
  ].join('\n')
  assert.deepEqual(titles({ headings: headingsIn(src, 'main.tex') }), ['Real'])
})

test('resolves includes from the main file folder, adding .tex', () => {
  assert.equal(resolveInclude('main.tex', 'intro'), 'intro.tex')
  assert.equal(resolveInclude('paper/main.tex', 'chapters/intro'), 'paper/chapters/intro.tex')
  assert.equal(resolveInclude('paper/main.tex', 'sections/a.tex'), 'paper/sections/a.tex')
  assert.equal(resolveInclude('paper/main.tex', '../shared/x'), 'shared/x.tex')
  assert.equal(resolveInclude('paper/main.tex', './a'), 'paper/a.tex')
  assert.equal(resolveInclude('paper/main.tex', '../../x'), null)
  assert.equal(resolveInclude('main.tex', '/etc/x'), null)
})

test('places included headings where the \\input is, in a subfolder main', async () => {
  const o = await buildOutline(
    'paper/main.tex',
    reader({
      'paper/main.tex':
        '\\section{Before}\n\\input{chapters/intro}\n\\include{chapters/end.tex}\n\\input appendix\n\\section{After}',
      // \input in an included file is still relative to the main file's folder.
      'paper/chapters/intro.tex': '\\section{Intro}\n\\input{chapters/detail}',
      'paper/chapters/detail.tex': '\\subsection{Detail}',
      'paper/chapters/end.tex': '\\section{End}',
      'paper/appendix.tex': '\\section{Appendix}',
    }),
  )
  assert.deepEqual(titles(o), ['Before', 'Intro', 'Detail', 'End', 'Appendix', 'After'])
  assert.deepEqual(
    o.headings.map((h) => h.path),
    [
      'paper/main.tex',
      'paper/chapters/intro.tex',
      'paper/chapters/detail.tex',
      'paper/chapters/end.tex',
      'paper/appendix.tex',
      'paper/main.tex',
    ],
  )
})

test('stops at cycles and self-includes, skips missing files and commented includes', async () => {
  const o = await buildOutline(
    'main.tex',
    reader({
      'main.tex': '\\section{Main}\n\\input{main}\n\\input{a}\n\\input{missing}\n% \\input{b}\n\\section{Last}',
      'a.tex': '\\section{A}\n\\input{b}',
      'b.tex': '\\section{B}\n\\input{a}',
    }),
  )
  assert.deepEqual(titles(o), ['Main', 'A', 'B', 'Last'])
  // b is only reached through a: the commented \input doesn't expand it a second time anyway.
  assert.equal(o.headings.filter((h) => h.title === 'B').length, 1)
})

test('no main file content: empty outline', async () => {
  assert.deepEqual((await buildOutline('main.tex', reader({}))).headings, [])
})

test('current heading follows the cursor, across included files', async () => {
  const o = await buildOutline(
    'main.tex',
    reader({
      'main.tex': 'preamble\n\\section{One}\ntext\n\\input{ch}\nmore\n\\section{Three}\n',
      'ch.tex': 'lead-in\n\\section{Two}\nbody',
    }),
  )
  assert.deepEqual(titles(o), ['One', 'Two', 'Three'])
  const at = (path: string, line: number) => o.headings[currentHeading(o, path, line)]?.title
  assert.equal(at('main.tex', 1), undefined) // before any heading
  assert.equal(at('main.tex', 2), 'One') // on the heading line
  assert.equal(at('main.tex', 3), 'One')
  assert.equal(at('main.tex', 5), 'Two') // after the \input: the last heading it brought in
  assert.equal(at('main.tex', 6), 'Three')
  assert.equal(at('ch.tex', 1), 'One') // included file, before its first heading
  assert.equal(at('ch.tex', 3), 'Two')
  assert.equal(currentHeading(o, 'other.tex', 1), -1)
})

test('depth follows the hierarchy, without empty steps for skipped levels', () => {
  const src = [
    '\\section{A}',
    '\\subsection{A1}',
    '\\paragraph{A1 note}',
    '\\section{B}',
    '\\paragraph{B note}',
    '\\subsubsection{B deep}',
    '\\chapter{C}',
  ].join('\n')
  assert.deepEqual(depths(headingsIn(src, 'main.tex')), [0, 1, 2, 0, 1, 1, 0])
})

test('titles with formatting nested at any depth, and escaped braces', () => {
  const src = [
    '\\section{Title with \\textbf{\\emph{nested formatting}}}',
    '\\subsection{A \\{literal\\} brace}',
    '\\section{Never closed',
    '\\section{After}',
  ].join('\n')
  const hs = headingsIn(src, 'main.tex')
  assert.deepEqual(hs.map((h) => [h.title, h.line]).slice(0, 2), [
    ['Title with nested formatting', 1],
    ['A literal brace', 2], // escaped braces don't end the title
  ])
  // An unclosed title swallows the rest, as TeX would; it never throws.
  assert.ok(hs.length >= 2)
})

// Review of #16: rename a heading in an included file, save, close its tab,
// and the outline must show the new heading, not the content fetched before.
test('a saved file replaces what the outline cached for it', async () => {
  const server: Record<string, string> = { 'main.tex': '\\input{ch}', 'ch.tex': '\\section{Old}' }
  let fetches = 0
  const cache = new OutlineCache(async (p) => {
    fetches++
    return server[p] ?? null
  })
  const open = new Map<string, string>()
  const read = async (p: string) => open.get(p) ?? cache.read(p)

  assert.deepEqual(titles(await buildOutline('main.tex', read)), ['Old'])
  open.set('ch.tex', '\\section{New}') // opened and edited
  assert.deepEqual(titles(await buildOutline('main.tex', read)), ['New'])
  server['ch.tex'] = open.get('ch.tex')!
  cache.saved('ch.tex', open.get('ch.tex')!) // the autosave
  open.delete('ch.tex') // tab closed
  const o = await buildOutline('main.tex', read)
  assert.deepEqual(titles(o), ['New'])
  assert.equal(o.headings[0].line, 1)
  assert.equal(fetches, 2) // main.tex and ch.tex once each: the save didn't refetch

  cache.clear() // the file list changed
  await buildOutline('main.tex', read)
  assert.equal(fetches, 4)
})

test('a failed fetch reads as a missing file', async () => {
  const cache = new OutlineCache(async () => {
    throw new Error('offline')
  })
  assert.equal(await cache.read('x.tex'), null)
})

test('document and hierarchical section counts follow includes from the main directory', async () => {
  const files = {
    'paper/main.tex': String.raw`\documentclass{article}
\begin{document}Opening words.
\section{First}One two. \input{chapters/child} After child.
\section{Last}Final.\end{document}`,
    'paper/chapters/child.tex': String.raw`Before subsection.\subsection{Child}Three four. \input tail`,
    'paper/tail.tex': String.raw`Five $ignored math$ six. % not counted
\verb|seven eight|`,
  }
  const out = await buildOutline('paper/main.tex', reader(files), defaultRules)
  assert.deepEqual(titles(out), ['First', 'Child', 'Last'])
  assert.deepEqual(out.counts, { total: 18, sections: [14, 9, 2], incomplete: false })
  const noTitles = await buildOutline('paper/main.tex', reader(files), { ...defaultRules, titles: false })
  assert.deepEqual(noTitles.counts, { total: 15, sections: [12, 8, 1], incomplete: false })
})

test('unsectioned prose counts, and unreadable or cyclic includes mark the count as partial', async () => {
  const out = await buildOutline('main.tex', reader({ 'main.tex': 'Just words.' }), defaultRules)
  assert.deepEqual(out.counts, { total: 2, sections: [], incomplete: false })
  const partial = await buildOutline(
    'main.tex',
    reader({ 'main.tex': String.raw`\section{One}Text.\input{missing}\input{main}` }),
    defaultRules,
  )
  assert.deepEqual(partial.counts, { total: 2, sections: [2], incomplete: true })
  assert.equal((await buildOutline('main.tex', reader({}))).counts, undefined)
})

test('headings and includes after the end of the document are ignored', async () => {
  const out = await buildOutline(
    'main.tex',
    reader({
      'main.tex': String.raw`\section{Real}One.\end{document}\section{Fake}\input{missing}`,
    }),
    defaultRules,
  )
  assert.deepEqual(titles(out), ['Real'])
  assert.deepEqual(out.counts, { total: 2, sections: [2], incomplete: false })
})

test('absolute and outside-project includes mark counts as partial', async () => {
  for (const include of ['/outside/chapter', '../outside']) {
    const out = await buildOutline('main.tex', reader({ 'main.tex': `Text.\\input{${include}}` }), defaultRules)
    assert.deepEqual(out.counts, { total: 1, sections: [], incomplete: true })
  }
})
