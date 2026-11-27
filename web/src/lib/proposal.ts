import { StateEffect, StateField, type EditorState } from '@codemirror/state'
import { Decoration, EditorView, WidgetType, type DecorationSet } from '@codemirror/view'
import { wordDiff, type Op } from './worddiff'

// A quick-action result waiting for accept/reject, shown in place as a word
// diff: removed text struck through, new text as widgets.
type ProposalOptions = {
  owner?: symbol
  isValid?: (state: EditorState, proposal: Proposal) => boolean
}
export type Proposal = ProposalOptions & { from: number; to: number; text: string; ops: Op[] }

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
    if (!value) return null
    let { p, deco } = value
    if (tr.docChanged) {
      // An edit inside the range makes the proposal stale; elsewhere it just moves.
      let touched = false
      tr.changes.iterChangedRanges((a, b) => {
        if (a <= p.to && b >= p.from) touched = true
      })
      if (touched) return null
      p = { ...p, from: tr.changes.mapPos(p.from, 1), to: tr.changes.mapPos(p.to, -1) }
      deco = deco.map(tr.changes)
    }
    if (p.isValid && !p.isValid(tr.state, p)) return null
    return tr.docChanged ? { p, deco } : value
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v?.deco ?? Decoration.none),
})

export const proposal = (state: EditorState) => state.field(proposalField, false)?.p ?? null

export function propose(view: EditorView, from: number, to: number, text: string, options?: ProposalOptions) {
  view.dispatch({
    effects: setProposal.of({ from, to, text, ops: wordDiff(view.state.sliceDoc(from, to), text), ...options }),
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
