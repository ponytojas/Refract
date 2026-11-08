// Run with: node --test web/src/lib/outline.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildOutline, currentHeading, depths, headingsIn, resolveInclude } from './outline.ts'

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
