import { useEffect, useReducer, useRef, useState } from 'react'
import { basicSetup } from 'codemirror'
import { Compartment, EditorSelection, EditorState, Prec } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { latex } from '@/lib/editor'
import { getPrefs, usePrefs } from '@/lib/prefs'
import { accept, proposalField, propose, reject } from '@/lib/proposal'
import type { CiteTarget } from './CiteDialog'
import QuickActions, { actions, type QuickHandle } from './QuickActions'
import { useCommands } from '@/lib/commands'
import {
  BookMarked,
  ClipboardPaste,
  Copy,
  FileSearch,
  MessageSquareText,
  Pencil,
  Play,
  Scissors,
  TextSelect,
} from 'lucide-react'
import { WithContextMenu, type MenuEntry } from '@/components/Menu'
import { mod } from '@/lib/shortcuts'
import WordCounter, { type LimitTarget } from './WordCounter'
import { sectionAt } from '@/lib/wordcount'
import { wordLimits, wordCountEnabled, excessWords, loadLimits, saveLimits, setLimits } from '@/lib/wordlimits'
import { t } from '@/lib/i18n'

const wrapping = new Compartment()
const counter = new Compartment()
const wrapExt = (on: boolean) => (on ? EditorView.lineWrapping : [])

type Props = {
  projectId: string
  countEnabled: boolean
  onWordCount: (count: number | null) => void
  onEnableCount: () => void
  path: string
  // Owned by the parent (it removes entries for closed/deleted files and moves them on rename).
  // Kept outside React so switching tabs keeps undo history and selection.
  states: Map<string, EditorState>
  initial: (path: string) => string
  onChange: (path: string, content: string) => void
  onSave: () => void
  onCompile: () => void
  onSync: (path: string, line: number, col: number) => void // forward sync (Mod-J)
  onSelect?: (lines: string) => void // selected lines ("3" or "3–9"), "" when nothing is selected
  jump?: { path: string; line: number; n: number } // n changes on every request
  insert?: { path: string; text: string } // replace the selection with text, then onInserted
  onInserted?: () => void
  quick?: boolean // show the quick-actions toolbar on selections (AI available)
  onCite?: (target: CiteTarget, suggest: boolean) => void // find/add a reference for the selection
  proposal?: { path: string; from: number; to: number; original: string; text: string } // show as an inline diff
  onProposed?: (ok: boolean) => void // false: the text there changed meanwhile
}

// One EditorView; each open file keeps its own EditorState, swapped on tab switch.
export default function Editor({
  projectId,
  countEnabled,
  onWordCount,
  onEnableCount,
  path,
  states,
  initial,
  onChange,
  onSave,
  onCompile,
  onSync,
  onSelect,
  jump,
  insert,
  onInserted,
  quick,
  onCite,
  proposal,
  onProposed,
}: Props) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const current = useRef(path)
  const cb = useRef({ onChange, onSave, onCompile, onSync, onSelect })
  cb.current = { onChange, onSave, onCompile, onSync, onSelect }

  const [mounted, setMounted] = useState<EditorView | null>(null)
  const [limitTarget, setLimitTarget] = useState<LimitTarget | null>(null)
  const contextPos = useRef<number | null>(null)
  const [dragging, setDragging] = useState(false) // no toolbar while a mouse selection is being made
  const [, bump] = useReducer((n: number) => n + 1, 0) // re-render the toolbar (it reads the view)
  const quickRef = useRef<QuickHandle>(null)

  // ⌘K with text selected: the quick actions, and the query as a free instruction.
  useCommands((query) => {
    const v = view.current
    if (!v || v.state.selection.main.empty) return []
    const counter = {
      id: 'sel:count',
      group: 'Selection',
      label: t('Count words'),
      icon: TextSelect,
      run: onEnableCount,
    }
    if (!quick) return [counter]
    const q = query.trim()
    return [
      counter,
      ...(q
        ? [
            {
              id: 'sel:ask',
              group: 'Selection',
              label: t('Ask: “{query}”', { query: q }),
              hint: t('edit the selection'),
              icon: MessageSquareText,
              pinned: true,
              run: () => quickRef.current?.run('custom', q),
            },
          ]
        : []),
      ...actions.map((a) => ({
        id: `sel:${a.id}`,
        group: 'Selection',
        label: a.command,
        hint: t('selection'),
        icon: a.icon,
        run: () => quickRef.current?.run(a.id),
      })),
      ...(onCite
        ? [
            {
              id: 'sel:cite',
              group: 'Selection',
              label: t('Find a citation for it'),
              hint: t('selection'),
              icon: BookMarked,
              run: () => cite(true),
            },
          ]
        : []),
    ]
  })

  // The selection as a citation target: the dialog suggests papers and places \cite in it.
  function cite(suggest: boolean) {
    const v = view.current
    if (!v || !onCite) return
    const { from, to } = v.state.selection.main
    const doc = v.state.doc
    onCite(
      {
        path: current.current,
        from,
        to,
        text: doc.sliceString(from, to),
        before: doc.sliceString(Math.max(0, from - 2000), from),
        after: doc.sliceString(to, Math.min(doc.length, to + 2000)),
      },
      suggest && from !== to,
    )
  }

  const { wrap, wordCountTitles } = usePrefs()
  const extensions = useRef([
    basicSetup,
    latex,
    proposalField,
    wordLimits,
    excessWords,
    counter.of(wordCountEnabled.of(countEnabled)),
    wrapping.of(wrapExt(getPrefs().wrap)),
    // Highest precedence: basicSetup binds Mod-Enter to "insert blank line".
    Prec.highest(
      keymap.of([
        { key: 'Tab', run: accept },
        { key: 'Escape', run: (v) => reject(v) || (quickRef.current?.cancel() ?? false) },
        {
          key: 'Mod-s',
          run: () => {
            cb.current.onSave()
            return true
          },
        },
        {
          key: 'Mod-Enter',
          run: () => {
            cb.current.onCompile()
            return true
          },
        },
        {
          key: 'Mod-j',
          run: (v) => {
            const head = v.state.selection.main.head
            const line = v.state.doc.lineAt(head)
            cb.current.onSync(current.current, line.number, head - line.from + 1)
            return true
          },
        },
      ]),
    ),
    EditorView.updateListener.of((u) => {
      // Keep the map live, so tab switches and renames always see the latest state.
      if (states.has(current.current)) states.set(current.current, u.state)
      if (u.docChanged || u.startState.field(wordLimits) !== u.state.field(wordLimits)) {
        saveLimits(projectId, current.current, u.state.doc.toString(), u.state.field(wordLimits))
        bump()
      }
      if (u.docChanged) cb.current.onChange(current.current, u.state.doc.toString())
      if (
        u.selectionSet ||
        u.focusChanged ||
        u.geometryChanged ||
        u.viewportChanged ||
        u.startState.field(proposalField) !== u.state.field(proposalField)
      )
        bump()
      if (u.selectionSet || u.docChanged) {
        const { from, to, empty } = u.state.selection.main
        const a = u.state.doc.lineAt(from).number
        const b = u.state.doc.lineAt(to).number
        cb.current.onSelect?.(empty ? '' : a === b ? `${a}` : `${a}–${b}`)
      }
    }),
  ]).current

  const stateFor = (p: string) => {
    let s = states.get(p)
    if (!s) {
      const doc = initial(p)
      s = EditorState.create({ doc, extensions })
      s = s.update({ effects: setLimits.of(loadLimits(projectId, p, doc)) }).state
      states.set(p, s)
    }
    return s
  }

  useEffect(() => {
    const v = new EditorView({ parent: host.current!, state: stateFor(path) })
    view.current = v
    setMounted(v)
    let frame = 0
    const onScroll = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(bump)
    }
    const down = () => setDragging(true)
    const up = () => setDragging(false)
    v.scrollDOM.addEventListener('scroll', onScroll)
    v.contentDOM.addEventListener('pointerdown', down)
    window.addEventListener('pointerup', up)
    return () => {
      window.removeEventListener('pointerup', up)
      cancelAnimationFrame(frame)
      v.destroy()
    }
  }, [])

  useEffect(() => {
    const v = view.current!
    const next = stateFor(path)
    current.current = path
    setLimitTarget(null)
    if (v.state !== next) v.setState(next)
    bump()
    v.focus()
  }, [path])

  // Stored states keep the setting they were created with: re-apply it to whichever is shown.
  useEffect(() => {
    view.current!.dispatch({ effects: wrapping.reconfigure(wrapExt(wrap)) })
  }, [wrap, path])

  useEffect(() => {
    view.current!.dispatch({ effects: counter.reconfigure(wordCountEnabled.of(countEnabled)) })
    setLimitTarget(null)
  }, [countEnabled, path])

  useEffect(() => {
    const v = view.current!
    const limits = v.state.field(wordLimits)
    if (limits.rules.titles !== wordCountTitles)
      v.dispatch({ effects: setLimits.of({ ...limits, rules: { ...limits.rules, titles: wordCountTitles } }) })
  }, [wordCountTitles, path])

  useEffect(() => {
    const v = view.current!
    if (!jump || jump.path !== path) return
    const line = v.state.doc.line(Math.min(Math.max(jump.line, 1), v.state.doc.lines))
    v.dispatch({
      selection: EditorSelection.cursor(line.from),
      effects: EditorView.scrollIntoView(line.from, { y: 'center' }),
    })
    v.focus()
  }, [jump, path])

  useEffect(() => {
    const v = view.current!
    if (!insert || insert.path !== path) return
    v.dispatch(v.state.replaceSelection(insert.text), { scrollIntoView: true, userEvent: 'input' })
    v.focus()
    onInserted?.()
  }, [insert, path])

  useEffect(() => {
    const v = view.current!
    if (!proposal || proposal.path !== path) return
    const ok = proposal.to <= v.state.doc.length && v.state.sliceDoc(proposal.from, proposal.to) === proposal.original
    if (ok) propose(v, proposal.from, proposal.to, proposal.text)
    v.focus()
    onProposed?.(ok)
  }, [proposal, path])

  // Right click: clipboard, AI actions on the selection, sync and compile.
  const menu = (): MenuEntry[] => {
    const v = view.current
    if (!v) return []
    const sel = v.state.selection.main
    const text = v.state.sliceDoc(sel.from, sel.to)
    const pos = contextPos.current ?? sel.head
    const section = sectionAt(v.state.doc.toString(), pos)
    const saved = v.state.field(wordLimits).ranges.find((r) => !r.section && pos >= r.from && pos <= r.to)
    const target: LimitTarget | null =
      saved ??
      (section
        ? { name: section.name, from: section.from, to: section.to, section: section.id }
        : !sel.empty
          ? { name: t('Selected fragment'), from: sel.from, to: sel.to }
          : null)
    const edit = (insert: string, userEvent: string) => {
      v.dispatch(v.state.replaceSelection(insert), { userEvent, scrollIntoView: true })
      v.focus()
    }
    return [
      {
        label: t('Cut'),
        icon: Scissors,
        shortcut: `${mod}X`,
        disabled: sel.empty,
        onSelect: () => navigator.clipboard.writeText(text).then(() => edit('', 'delete.cut')),
      },
      {
        label: t('Copy'),
        icon: Copy,
        shortcut: `${mod}C`,
        disabled: sel.empty,
        onSelect: () => navigator.clipboard.writeText(text),
      },
      {
        label: t('Paste'),
        icon: ClipboardPaste,
        shortcut: `${mod}V`,
        onSelect: () => navigator.clipboard.readText().then((s) => edit(s, 'input.paste')),
      },
      {
        label: t('Select all'),
        icon: TextSelect,
        shortcut: `${mod}A`,
        onSelect: () => {
          v.dispatch({ selection: { anchor: 0, head: v.state.doc.length } })
          v.focus()
        },
      },
      '-',
      ...(countEnabled
        ? [{ label: t('Word limit…'), disabled: !target, onSelect: () => target && setLimitTarget(target) }]
        : []),
      ...(countEnabled && !sel.empty && section
        ? [
            {
              label: t('Word limit for selection…'),
              onSelect: () =>
                setLimitTarget(
                  v.state
                    .field(wordLimits)
                    .ranges.find((r) => !r.section && r.from === sel.from && r.to === sel.to) ?? {
                    name: t('Selected fragment'),
                    from: sel.from,
                    to: sel.to,
                  },
                ),
            },
          ]
        : []),
      ...(quick && !sel.empty
        ? [
            ...actions.map((a) => ({ label: a.command, icon: a.icon, onSelect: () => quickRef.current?.run(a.id) })),
            { label: t('Ask the assistant…'), icon: Pencil, onSelect: () => quickRef.current?.ask() },
            ...(onCite ? [{ label: t('Find a citation…'), icon: BookMarked, onSelect: () => cite(true) }] : []),
            '-' as const,
          ]
        : []),
      {
        label: t('Show in PDF'),
        icon: FileSearch,
        shortcut: `${mod}J`,
        onSelect: () => {
          const line = v.state.doc.lineAt(sel.head)
          cb.current.onSync(current.current, line.number, sel.head - line.from + 1)
        },
      },
      { label: t('Compile'), icon: Play, shortcut: `${mod}↵`, onSelect: () => cb.current.onCompile() },
    ]
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <div
        className="relative min-h-0 flex-1"
        onContextMenuCapture={(e) => {
          contextPos.current = view.current?.posAtCoords({ x: e.clientX, y: e.clientY }) ?? null
        }}
      >
        <WithContextMenu items={menu} className="h-full select-text">
          <div className="h-full" ref={host} />
        </WithContextMenu>
        {mounted && countEnabled && (
          <WordCounter
            key={path}
            view={mounted}
            path={path}
            ai={!!quick}
            enabled={countEnabled}
            onCount={onWordCount}
            target={limitTarget}
            onClose={() => setLimitTarget(null)}
          />
        )}
        {quick && mounted && (
          <QuickActions
            view={mounted}
            path={path}
            dragging={dragging}
            handle={quickRef}
            onCite={onCite ? () => cite(true) : undefined}
          />
        )}
      </div>
    </div>
  )
}
