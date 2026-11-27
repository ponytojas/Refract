import { test } from 'node:test'
import assert from 'node:assert/strict'
import { history, undo, redo } from '@codemirror/commands'
import { EditorState, type Transaction } from '@codemirror/state'
import { defaultRules, findSections } from './wordcount.ts'
import {
  excessWords,
  limitAt,
  loadLimits,
  moveLimits,
  saveLimits,
  setLimits,
  shortenRequest,
  wordLimits,
} from './wordlimits.ts'

const limits = (state: EditorState) => state.field(wordLimits)
const create = (source = 'one two three') => EditorState.create({ doc: source, extensions: [wordLimits, excessWords] })

test('manual limits follow edits before and inside the range, including boundaries', () => {
  let state = create()
  state = state.update({
    effects: setLimits.of({ rules: defaultRules, ranges: [{ id: 'a', name: 'Test', from: 4, to: 7, max: 1 }] }),
  }).state
  state = state.update({ changes: { from: 0, insert: 'start ' } }).state
  assert.equal(limits(state).ranges[0].from, 10)
  state = state.update({ changes: { from: 13, insert: ' more' } }).state
  const range = limits(state).ranges[0]
  assert.equal(state.sliceDoc(range.from, range.to), 'two more')
  assert.equal(state.field(excessWords).size, 1)
  state = state.update({ changes: { from: range.from, to: range.to, insert: 'short' } }).state
  assert.equal(state.field(excessWords).size, 0)
})

test('automatic limits follow the section body and retain the limit when its title changes', () => {
  let state = create(String.raw`\section{Intro}one two\section{Next}three`)
  const section = findSections(state.doc.toString())[0]
  state = state.update({
    effects: setLimits.of({ rules: defaultRules, ranges: [{ ...section, section: section.id, max: 1 }] }),
  }).state
  const at = state.doc.toString().indexOf('Intro')
  state = state.update({ changes: { from: at, to: at + 5, insert: 'Introduction' } }).state
  assert.equal(limits(state).ranges.length, 1)
  const range = limits(state).ranges[0]
  assert.equal(state.sliceDoc(range.from, range.to), 'one two')
  assert.equal(range.section, 'section:Introduction:0')
  assert.equal(range.name, 'Introduction')
})

test('edits spanning part of a heading and its body keep the limit on that heading', () => {
  for (const nextTitle of ['Next', 'Intro']) {
    const source = String.raw`\section{Intro}one two\section{${nextTitle}}three`
    const [first] = findSections(source)
    for (const [selected, insert] of [
      ['}one', '}uno'],
      ['tro}one', 'tro}uno'],
    ]) {
      let state = create(source)
      const saved = { id: 'first', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
      state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
      const from = source.indexOf(selected)
      state = state.update({ changes: { from, to: from + selected.length, insert } }).state
      assert.deepEqual(limits(state).ranges, [saved])
      assert.equal(state.sliceDoc(saved.from, saved.to), 'uno two')
    }
  }
})

test('a partial rewrite keeps the limit on the surviving heading when it inserts another with the same title', () => {
  const heading = String.raw`\section{Same}`
  const source = `${heading}one two`
  let state = create(source)
  const [first] = findSections(source)
  const saved = { id: 'first', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
  state = state.update({
    changes: { from: first.from - 1, to: source.length, insert: `}new body${heading}three` },
  }).state
  const [rewritten] = findSections(state.doc.toString())
  assert.deepEqual(limits(state).ranges, [{ ...saved, to: rewritten.to }])
})

test('joining fragments of two identical headings does not transfer either limit', () => {
  const source = String.raw`\section{Same}one\section{Same}two`
  let state = create(source)
  const ranges = findSections(source).map((section) => ({ ...section, section: section.id, max: 1 }))
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges }) }).state
  const from = source.indexOf('Same') + 1
  const to = source.lastIndexOf('Same') + 3
  state = state.update({ changes: { from, to, insert: 'am' } }).state
  assert.equal(state.doc.toString(), String.raw`\section{Same}two`)
  assert.deepEqual(limits(state).ranges, [])
})

test('a quick action rewriting the heading and body keeps the limit on the rewritten section', () => {
  const heading = String.raw`\section{Intro}`
  for (const preamble of ['', 'Preamble\n']) {
    const source = `${preamble}${heading}one two\n${heading}three four`
    let state = create(source)
    const [first] = findSections(source)
    const saved = { id: 'a', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
    state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
    state = state.update({
      changes: { from: 0, to: first.to - 1, insert: `${preamble}${heading}better words` },
      userEvent: 'input.quick',
    }).state
    const [rewritten] = findSections(state.doc.toString())
    assert.deepEqual(limits(state).ranges, [{ ...saved, from: rewritten.from, to: rewritten.to }])
    assert.equal(state.sliceDoc(rewritten.from, rewritten.to), 'better words\n')
    assert.equal(state.field(excessWords).size, 1)
  }
})

test('rewriting a span with ambiguous repeated headings does not transfer the limit', () => {
  const heading = String.raw`\section{Same}`
  const single = `${heading}one two`
  const repeated = `${single}\n${heading}three four`
  for (const [source, insert] of [
    [single, repeated],
    [repeated, single],
  ]) {
    let state = create(source)
    const [first] = findSections(source)
    const saved = { id: 'a', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
    state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
    state = state.update({ changes: { from: 0, to: source.length, insert } }).state
    assert.deepEqual(limits(state).ranges, [])
  }
})

test('rule changes update red decorations, overlapping limits do not duplicate marks', () => {
  let state = create(String.raw`one \footnote{two three}`)
  const range = { id: 'a', name: 'A', from: 0, to: state.doc.length, max: 1 }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [range, { ...range, id: 'b' }] }) }).state
  assert.equal(state.field(excessWords).size, 2)
  state = state.update({
    effects: setLimits.of({ ...limits(state), rules: { ...defaultRules, footnotes: false } }),
  }).state
  assert.equal(state.field(excessWords).size, 0)
})

test('persistence is isolated per project and file; stale manual offsets are discarded', () => {
  const values = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    },
  })
  const ranges = [{ id: 'a', name: 'A', from: 0, to: 3, max: 2 }]
  saveLimits('project', 'main.tex', 'one', { rules: defaultRules, ranges })
  assert.deepEqual(loadLimits('project', 'main.tex', 'one').ranges, ranges)
  assert.equal(loadLimits('other', 'main.tex', 'one').ranges.length, 0)
  assert.equal(loadLimits('project', 'other.tex', 'one').ranges.length, 0)
  assert.equal(loadLimits('project', 'main.tex', 'changed').ranges.length, 0)
  const source = String.raw`\subsection{Contexto}one two`
  const section = findSections(source)[0]
  saveLimits('project', 'main.tex', source, {
    rules: defaultRules,
    ranges: [{ ...section, section: section.id, name: 'Con104texto', max: 1 }],
  })
  assert.equal(loadLimits('project', 'main.tex', source).ranges[0].name, 'Contexto')
})

test('budget targeting includes headings, prefers nested budgets and has no outside fallback', () => {
  const source = String.raw`Preamble\section{Main}one two\subsection{Child}three four\section{Next}five six\end{document}`
  const ranges = findSections(source).map((s) => ({ ...s, section: s.id, max: 1 }))
  for (const name of ['Main', 'Child', 'Next']) assert.equal(limitAt(source, ranges, source.indexOf(name))?.name, name)
  assert.equal(limitAt(source, ranges, source.indexOf('three'))?.name, 'Child')
  assert.equal(limitAt(source, ranges, source.indexOf('one'))?.name, 'Main')
  assert.equal(limitAt(source, ranges, 0), undefined)
  assert.equal(limitAt(source, ranges, source.length), undefined)
  // A non-exceeded child still takes precedence over an exceeded parent.
  ranges[1].max = 100
  assert.equal(limitAt(source, ranges, source.indexOf('three'))?.max, 100)
})

test('file and folder renames persist limits for closed files and preserve unrelated paths', () => {
  const values = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      get length() {
        return values.size
      },
      key: (i: number) => [...values.keys()][i] ?? null,
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    },
  })
  const saved = { rules: defaultRules, ranges: [{ id: 'a', name: 'A', from: 0, to: 3, max: 2 }] }
  for (const path of ['main.tex', 'chapters/a.tex', 'chapters/nested/b.tex', 'chapters-other/c.tex'])
    saveLimits('project', path, 'one', saved)
  saveLimits('other', 'chapters/a.tex', 'one', saved)
  moveLimits('project', 'main.tex', 'renamed.tex')
  moveLimits('project', 'chapters', 'renamed')
  for (const path of ['renamed.tex', 'renamed/a.tex', 'renamed/nested/b.tex', 'chapters-other/c.tex'])
    assert.deepEqual(loadLimits('project', path, 'one'), saved)
  for (const path of ['main.tex', 'chapters/a.tex', 'chapters/nested/b.tex'])
    assert.equal(loadLimits('project', path, 'one').ranges.length, 0)
  assert.deepEqual(loadLimits('other', 'chapters/a.tex', 'one'), saved)
})

test('undo and redo restore and remove the budget on the original heading when titles repeat', () => {
  const source = String.raw`\section{Same}one two\section{Same}three four`
  let state = EditorState.create({ doc: source, extensions: [history(), wordLimits, excessWords] })
  const [first, second] = findSections(source)
  const saved = { id: 'first', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
  state = state.update({ changes: { from: first.headingFrom, to: first.from } }).state
  assert.equal(limits(state).ranges.length, 0)
  assert.equal(state.field(excessWords).size, 0)
  const editor = {
    get state() {
      return state
    },
    dispatch: (tr: Transaction) => {
      state = tr.state
    },
  }
  for (let i = 0; i < 2; i++) {
    assert.equal(undo(editor), true)
    assert.equal(state.doc.toString(), source)
    assert.deepEqual(limits(state).ranges, [saved])
    assert.equal(limitAt(source, limits(state).ranges, second.headingFrom), undefined)
    assert.equal(state.field(excessWords).size, 1)
    assert.equal(redo(editor), true)
    assert.equal(limits(state).ranges.length, 0)
    assert.equal(state.field(excessWords).size, 0)
  }
})

test('deleting an adjacent heading leaves the preceding section without that budget', () => {
  const source = String.raw`\section{Same}\section{Same}three four`
  let state = create(source)
  const [, second] = findSections(source)
  const saved = { id: 'second', name: second.name, from: second.from, to: second.to, section: second.id, max: 1 }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
  state = state.update({ changes: { from: second.headingFrom, to: second.from } }).state
  assert.deepEqual(limits(state).ranges, [])
})

test('grouped title edits and heading deletion restore the original budget on undo', () => {
  const source = String.raw`\section{Intro}one two`
  let state = EditorState.create({ doc: source, extensions: [history(), wordLimits] })
  const section = findSections(source)[0]
  const saved = { ...section, section: section.id, max: 1 }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [saved] }) }).state
  state = state.update({
    changes: { from: 9, to: 14, insert: 'Introduction' },
    userEvent: 'input.type',
    time: 100,
  }).state
  state = state.update({
    changes: { from: 0, to: findSections(state.doc.toString())[0].from },
    userEvent: 'input.type',
    time: 101,
  }).state
  assert.equal(limits(state).ranges.length, 0)
  assert.equal(
    undo({
      state,
      dispatch: (tr) => {
        state = tr.state
      },
    }),
    true,
  )
  assert.equal(state.doc.toString(), source)
  assert.deepEqual(limits(state).ranges, [saved])
})

test('undo recovers a stale section occurrence at its saved body position', () => {
  const source = String.raw`\section{Intro}one two\section{Next}three`
  let state = EditorState.create({ doc: source, extensions: [history(), wordLimits] })
  const [first, next] = findSections(source)
  const saved = { id: 'intro', name: first.name, from: first.from, to: first.to, section: first.id, max: 1 }
  state = state.update({
    effects: setLimits.of({
      rules: defaultRules,
      ranges: [
        { ...saved, section: 'section:Intro:9' },
        { ...saved, id: 'unrelated', from: next.from, to: next.to, section: 'section:Missing:0' },
      ],
    }),
  }).state
  state = state.update({ changes: { from: first.headingFrom, to: first.from } }).state
  assert.deepEqual(limits(state).ranges, [])
  assert.equal(
    undo({
      state,
      dispatch: (tr) => {
        state = tr.state
      },
    }),
    true,
  )
  assert.deepEqual(limits(state).ranges, [saved])
})

test('a selection ending at the next heading keeps the child budget and considers its full range', () => {
  const source = String.raw`\section{Main}Outer\subsection{Child}Child text\subsection{Sibling}Other text`
  const ranges = findSections(source)
    .slice(0, 2)
    .map((s) => ({ ...s, section: s.id, max: 1 }))
  const [parent, child] = ranges
  assert.equal(limitAt(source, ranges, child.headingFrom, child.to)?.id, child.id)
  assert.equal(limitAt(source, ranges, child.from, child.to)?.id, child.id)
  assert.equal(limitAt(source, ranges, child.to)?.id, parent.id)
  assert.equal(limitAt(source, ranges, parent.from, child.to)?.id, parent.id)
  assert.equal(limitAt(source, ranges, child.from, child.to + 1)?.id, parent.id)
})

test('a subsection within its own limit overrides its exceeded parent highlighting', () => {
  const intro =
    'Este texto contiene suficientes palabras para superar un limite pequeno y comprobar el resaltado de las palabras que sobran.'
  const child = 'Esta subseccion tiene su propio texto.'
  const source = String.raw`\section{Introduccion}${intro}\subsection{Contexto}${child}`
  let state = create(source)
  const ranges = findSections(source).map((s, i) => ({ ...s, section: s.id, max: i ? 104 : 11 }))
  const marked = () => {
    const text: string[] = []
    state.field(excessWords).between(0, state.doc.length, (from, to) => {
      text.push(state.sliceDoc(from, to))
    })
    return text
  }
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges }) }).state
  const introExcess = ['comprobar', 'el', 'resaltado', 'de', 'las', 'palabras', 'que', 'sobran']
  assert.deepEqual(marked(), introExcess)
  // Limit order must not affect priority.
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [...ranges].reverse() }) }).state
  assert.deepEqual(marked(), introExcess)
  state = state.update({
    effects: setLimits.of({ rules: defaultRules, ranges: [ranges[0], { ...ranges[1], max: 2 }] }),
  }).state
  assert.deepEqual(marked(), [...introExcess, 'tiene', 'su', 'propio', 'texto'])
  state = state.update({ effects: setLimits.of({ rules: defaultRules, ranges: [ranges[0]] }) }).state
  assert.deepEqual(marked(), [...introExcess, 'Contexto', 'Esta', 'subseccion', 'tiene', 'su', 'propio', 'texto'])
})

test('shortening a limited range sends the project instructions like every quick action', () => {
  const state = EditorState.create({ doc: 'Intro. ' + 'word '.repeat(20) + 'End.' })
  const range = { id: 'r', name: 'Abstract', max: 10, from: 7, to: 107 }
  const body = shortenRequest(state, range, defaultRules, 'main.tex', '', 'Use British spelling.')
  assert.equal(body.projectInstructions, 'Use British spelling.')
  assert.equal(body.action, 'custom')
  assert.equal(body.selection, state.sliceDoc(7, 107))
  assert.equal(body.before, 'Intro. ')
  assert.equal(body.after, 'End.')
  assert.match(body.instruction, /at most 10 readable words/)
  assert.equal(shortenRequest(state, range, defaultRules, 'main.tex', '', undefined).projectInstructions, undefined)
})
