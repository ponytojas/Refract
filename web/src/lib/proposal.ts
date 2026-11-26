import { StateEffect, StateField, type EditorState } from '@codemirror/state'
import { Decoration, EditorView, WidgetType, type DecorationSet } from '@codemirror/view'
import { wordDiff, type Op } from './worddiff'

// A quick-action result waiting for accept/reject, shown in place as a word
// diff: removed text struck through, new text as widgets.
export type Proposal = { from: number; to: number; text: string; ops: Op[]; owner?: symbol }

export const setProposal = StateEffect.define<Proposal | null>()

class Inserted extends WidgetType {
  constructor(readonly text: string) {
    super()
  }
  eq(other: Inserted) {
    return other.text === this.text
  }
  toDOM() {
    const el = document.createElement('span')
    el.className = 'cm-quick-ins'
    el.textContent = this.text
    return el
  }
}

function decorations(p: Proposal): DecorationSet {
  const ranges = []
  let pos = p.from
  for (const o of p.ops) {
    if (o.op === 'ins') ranges.push(Decoration.widget({ widget: new Inserted(o.text), side: 1 }).range(pos))
    else {
      if (o.op === 'del') ranges.push(Decoration.mark({ class: 'cm-quick-del' }).range(pos, pos + o.text.length))
      pos += o.text.length
    }
  }
  return Decoration.set(ranges, true)
}

export const proposalField = StateField.define<{ p: Proposal; deco: DecorationSet } | null>({
  create: () => null,
  update(value, tr) {
    for (const e of tr.effects) {
      if (e.is(setProposal)) return e.value ? { p: e.value, deco: decorations(e.value) } : null
    }
    if (!value || !tr.docChanged) return value
    // An edit inside the range makes the proposal stale; elsewhere it just moves.
    let touched = false
    tr.changes.iterChangedRanges((a, b) => {
      if (a <= value.p.to && b >= value.p.from) touched = true
    })
    if (touched) return null
    const p = { ...value.p, from: tr.changes.mapPos(value.p.from, 1), to: tr.changes.mapPos(value.p.to, -1) }
    return { p, deco: value.deco.map(tr.changes) }
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v?.deco ?? Decoration.none),
})

export const proposal = (state: EditorState) => state.field(proposalField, false)?.p ?? null

export function propose(view: EditorView, from: number, to: number, text: string, owner?: symbol) {
  view.dispatch({
    effects: setProposal.of({ from, to, text, ops: wordDiff(view.state.sliceDoc(from, to), text), owner }),
  })
}

// Accepting is a normal, undoable edit; the new text stays selected so another action can follow.
export function accept(view: EditorView): boolean {
  const p = proposal(view.state)
  if (!p) return false
  view.dispatch({
    changes: { from: p.from, to: p.to, insert: p.text },
    selection: { anchor: p.from, head: p.from + p.text.length },
    effects: setProposal.of(null),
    userEvent: 'input.quick',
    scrollIntoView: true,
  })
  return true
}

export function reject(view: EditorView): boolean {
  if (!proposal(view.state)) return false
  view.dispatch({ effects: setProposal.of(null) })
  return true
}
