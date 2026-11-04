import { invertedEffects } from '@codemirror/commands'
import { Facet, StateEffect, StateField } from '@codemirror/state'
import { Decoration, EditorView, type DecorationSet } from '@codemirror/view'
import { defaultRules, findSections, latexWords, selectedWords, type CountRules } from './wordcount.ts'

export type WordLimit = { id: string; name: string; max: number; from: number; to: number; section?: string }
export type Limits = { rules: CountRules; ranges: WordLimit[] }
export const setLimits = StateEffect.define<Limits>()
// History maps these positions when several edits are grouped into one undo.
const restoreLimits = StateEffect.define<WordLimit[]>({
  map: (ranges, changes) =>
    ranges.map((r) => ({
      ...r,
      from: changes.mapPos(r.from, -1),
      to: changes.mapPos(r.to, 1),
    })),
})
export const wordLimits = StateField.define<Limits>({
  create: () => ({ rules: { ...defaultRules }, ranges: [] }),
  update(value, tr) {
    for (const effect of tr.effects) if (effect.is(setLimits)) return effect.value
    const restored = tr.effects.flatMap((effect) => (effect.is(restoreLimits) ? effect.value : []))
    if (!tr.docChanged && !restored.length) return value
    const sections = findSections(tr.newDoc.toString())
    return {
      ...value,
      ranges: [...value.ranges, ...restored.filter((r) => !value.ranges.some((v) => v.id === r.id))].flatMap(
        (range) => {
          if (range.section) {
            const mapped = restored.includes(range) ? range.from : tr.changes.mapPos(range.from, -1)
            const section = sections.find((s) => s.from === mapped) ?? sections.find((s) => s.id === range.section)
            return section
              ? [{ ...range, name: section.name, section: section.id, from: section.from, to: section.to }]
              : []
          }
          const from = tr.changes.mapPos(range.from, -1)
          const to = tr.changes.mapPos(range.to, 1)
          return [{ ...range, from, to }]
        },
      ),
    }
  },
  provide: (field) =>
    invertedEffects.of((tr) => {
      if (!tr.docChanged) return []
      const remaining = new Set(tr.state.field(field).ranges.map((r) => r.id))
      const removed = tr.startState.field(field).ranges.filter((r) => !remaining.has(r.id))
      return removed.length ? [restoreLimits.of(removed)] : []
    }),
})

// Include headings and choose the smallest containing budget, even when it is not exceeded.
export function limitAt(source: string, ranges: WordLimit[], from: number, to = from): WordLimit | undefined {
  const sections = findSections(source)
  return ranges
    .map((range) => ({ range, start: sections.find((s) => s.id === range.section)?.headingFrom ?? range.from }))
    .filter(
      ({ range, start }) =>
        from >= start &&
        (from < to ? to <= range.to : from < range.to || (from === source.length && from === range.to)),
    )
    .sort((a, b) => a.range.to - a.start - (b.range.to - b.start))[0]?.range
}

export const wordCountEnabled = Facet.define<boolean, boolean>({ combine: (values) => values[0] ?? true })

export const excessWords = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, tr) {
    if (!tr.docChanged && tr.startState.field(wordLimits) === tr.state.field(wordLimits)) return value
    const { ranges, rules } = tr.state.field(wordLimits)
    if (!ranges.length) return Decoration.none
    const source = tr.newDoc.toString()
    const words = latexWords(source, rules)
    const sections = findSections(source)
    const excess = new Map<string, { from: number; to: number }>()
    for (const range of ranges) {
      // A nested limit owns its highlighting, including when it is below its maximum.
      const nested = ranges
        .filter((r) => r.from >= range.from && r.to <= range.to && (r.from > range.from || r.to < range.to))
        .map((r) => ({ from: sections.find((s) => s.id === r.section)?.headingFrom ?? r.from, to: r.to }))
      for (const word of selectedWords(words, range.from, range.to).slice(range.max)) {
        if (nested.some((r) => word.from >= r.from && word.to <= r.to)) continue
        excess.set(`${word.from}:${word.to}`, {
          from: Math.max(range.from, word.from),
          to: Math.min(range.to, word.to),
        })
      }
    }
    return Decoration.set(
      [...excess.values()]
        .filter((w) => w.to > w.from)
        .map((w) => Decoration.mark({ class: 'cm-word-excess' }).range(w.from, w.to)),
      true,
    )
  },
  provide: (field) =>
    EditorView.decorations.compute([field, wordCountEnabled], (state) =>
      state.facet(wordCountEnabled) ? state.field(field) : Decoration.none,
    ),
})

const key = (project: string, path: string) => `refract-wordlimits:${project}:${path}`
export function moveLimits(project: string, from: string, to: string) {
  try {
    const prefix = key(project, '')
    const entries = Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i))
      .filter((k): k is string => k !== null && (k === prefix + from || k.startsWith(prefix + from + '/')))
      .map((k) => [k, localStorage.getItem(k)] as const)
    for (const [oldKey, value] of entries) {
      if (value === null) continue
      localStorage.setItem(prefix + to + oldKey.slice((prefix + from).length), value)
      localStorage.removeItem(oldKey)
    }
  } catch {
    /* storage unavailable */
  }
}
export function saveLimits(project: string, path: string, source: string, limits: Limits) {
  try {
    localStorage.setItem(key(project, path), JSON.stringify({ source, ...limits }))
  } catch {
    /* storage unavailable */
  }
}
export function loadLimits(project: string, path: string, source: string): Limits {
  const fallback = { rules: { ...defaultRules }, ranges: [] }
  try {
    const data = JSON.parse(localStorage.getItem(key(project, path)) ?? 'null')
    if (!data || !Array.isArray(data.ranges)) return fallback
    const rules = { ...defaultRules }
    for (const k of Object.keys(rules) as (keyof CountRules)[])
      if (typeof data.rules?.[k] === 'boolean') rules[k] = data.rules[k]
    const sections = findSections(source)
    const ranges = (data.ranges as WordLimit[]).flatMap((r) => {
      if (!r || typeof r.id !== 'string' || typeof r.name !== 'string' || !Number.isSafeInteger(r.max) || r.max < 1)
        return []
      if (r.section) {
        const s = sections.find((s) => s.id === r.section)
        return s ? [{ ...r, name: s.name, from: s.from, to: s.to }] : []
      }
      // Don't silently apply stale offsets after changes outside this editor.
      return data.source === source &&
        Number.isInteger(r.from) &&
        Number.isInteger(r.to) &&
        r.from >= 0 &&
        r.to >= r.from &&
        r.to <= source.length
        ? [r]
        : []
    })
    return { rules, ranges }
  } catch {
    return fallback
  }
}
