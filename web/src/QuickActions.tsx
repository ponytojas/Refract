import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type Ref } from 'react'
import type { EditorView } from '@codemirror/view'
import {
  ArrowUp,
  BookMarked,
  BookOpen,
  Check,
  Pencil,
  RotateCcw,
  Scissors,
  Sparkles,
  SpellCheck,
  Square,
  Table,
  X,
} from 'lucide-react'
import { api, errorText } from './api'
import { Button } from '@/components/ui/button'
import { ShimmerText } from '@/components/ui/chatcn/shimmer-text'
import Markdown from '@/components/Markdown'
import { accept, proposal, propose, reject } from '@/lib/proposal'
import { getPrefs } from '@/lib/prefs'
import { cn } from '@/lib/utils'
import { t } from '@/lib/i18n'

export type Action = 'improve' | 'shorten' | 'grammar' | 'table' | 'explain' | 'custom'

// label: the toolbar button; command: the same action in the ⌘K palette.
export const actions: { id: Action; label: string; command: string; icon: typeof Sparkles }[] = [
  { id: 'improve', label: t('Improve'), command: t('Improve the wording'), icon: Sparkles },
  { id: 'shorten', label: t('Shorten'), command: t('Make it shorter'), icon: Scissors },
  { id: 'grammar', label: t('Fix grammar'), command: t('Fix grammar and spelling'), icon: SpellCheck },
  { id: 'table', label: t('Table'), command: t('Convert to a table'), icon: Table },
  { id: 'explain', label: t('Explain'), command: t('Explain it'), icon: BookOpen },
]

const working: Record<Action, string> = {
  improve: t('Improving'),
  shorten: t('Shortening'),
  grammar: t('Fixing grammar'),
  table: t('Building the table'),
  explain: t('Explaining'),
  custom: t('Editing'),
}

const noChanges = t('No changes to suggest.')
const CONTEXT = 2000 // characters of context on each side

type Job = { from: number; to: number; action: Action; instruction: string }

export type QuickHandle = {
  run: (action: Action, instruction?: string) => void // from the ⌘K palette
  ask: () => void // open the instruction field (context menu)
  cancel: () => boolean // Esc: stop a request or close a result
}

type Props = {
  view: EditorView
  path: string
  dragging: boolean
  handle: Ref<QuickHandle>
  onCite?: () => void // open "Add a reference" for the selection
  instructions?: () => Promise<string | undefined> // the project's REFRACT.md, as sent to the server
}

// Floating toolbar over the editor selection: one-shot AI edits (§4.7), shown
// as an inline diff to accept (Tab) or reject (Esc).
export default function QuickActions({ view, path, dragging, handle, onCite, instructions }: Props) {
  const [job, setJob] = useState<Job | null>(null) // running, or finished with an error/explanation
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [explanation, setExplanation] = useState('')
  const [asking, setAsking] = useState(false)
  const [instruction, setInstruction] = useState('')
  const [within, setWithin] = useState(false) // focus is inside the toolbar
  const abort = useRef<AbortController | null>(null)
  const bar = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const [left, setLeft] = useState(0)
  const [compact, setCompact] = useState(false)

  const pending = proposal(view.state)
  const sel = view.state.selection.main
  const range = pending ?? job ?? (sel.empty ? null : sel)

  function reset() {
    abort.current?.abort()
    setJob(null)
    setRunning(false)
    setError('')
    setExplanation('')
    setAsking(false)
  }

  async function run(action: Action, text = instruction) {
    const r = pending ?? (sel.empty ? null : sel)
    if (!r) return
    if (pending) reject(view)
    const { from, to } = r
    const doc = view.state.doc
    const j = { from, to, action, instruction: text }
    abort.current?.abort()
    const controller = new AbortController() // this run's own: a newer run replaces abort.current
    abort.current = controller
    setJob(j)
    setRunning(true)
    setError('')
    setExplanation('')
    setAsking(false)
    try {
      const projectInstructions = await instructions?.()
      if (controller.signal.aborted) return // cancelled or replaced while reading them
      const res = await api<{ text: string }>('/api/quick', {
        method: 'POST',
        signal: controller.signal,
        body: {
          action,
          instruction: text,
          selection: doc.sliceString(from, to),
          before: doc.sliceString(Math.max(0, from - CONTEXT), from),
          after: doc.sliceString(to, Math.min(doc.length, to + CONTEXT)),
          file: path,
          projectInstructions,
          model: getPrefs().model,
        },
      })
      if (action === 'explain') setExplanation(res.text)
      else if (res.text === doc.sliceString(from, to)) setError(noChanges)
      else if (view.state.sliceDoc(from, to) !== doc.sliceString(from, to))
        setError(t('The text changed while the assistant was working. Try again.'))
      else {
        propose(view, from, to, res.text)
        setJob(null)
        view.focus()
      }
    } catch (e) {
      if (controller.signal.aborted && abort.current !== controller) return // a newer run owns the state
      if ((e as Error).name !== 'AbortError') setError(errorText(e))
      else setJob(null)
    } finally {
      if (abort.current === controller) setRunning(false)
    }
  }

  useImperativeHandle(handle, () => ({
    ask() {
      setAsking(true)
      requestAnimationFrame(() => input.current?.focus())
    },
    run(action, text = '') {
      if (action === 'custom') setInstruction(text)
      run(action, text)
    },
    cancel() {
      if (!job && !asking) return false
      reset()
      view.focus()
      return true
    },
  }))

  // A new selection starts over (a running request keeps its own range).
  useEffect(() => {
    if (running) return
    setJob(null)
    setError('')
    setExplanation('')
    setAsking(false)
  }, [sel.from, sel.to])

  const visible = !dragging && (view.hasFocus || within || !!job || !!pending)
  const a = range && visible ? view.coordsAtPos(range.from) : null
  const box = view.dom.getBoundingClientRect()
  const above = a ? a.top - box.top - 44 : 0
  const below = a && range ? (view.coordsAtPos(range.to)?.bottom ?? a.bottom) - box.top + 8 : 0
  const top = above >= 4 ? above : below
  const wantLeft = a ? a.left - box.left : 0

  // Keep the toolbar inside the editor horizontally (its width depends on its
  // state and language). Too wide for the editor: icons only, labels in tooltips.
  const fullWidth = useRef(0)
  useLayoutEffect(() => {
    const w = bar.current?.offsetWidth ?? 0
    if (!compact) fullWidth.current = w
    const fits = fullWidth.current <= box.width - 16
    if (fits === compact) setCompact(!fits)
    setLeft(Math.max(8, Math.min(wantLeft, box.width - w - 8)))
  })

  if (!a) return null
  return (
    <div
      ref={bar}
      className="absolute z-30 flex flex-col items-start gap-1.5"
      style={{ top, left }}
      onFocus={() => setWithin(true)}
      onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && setWithin(false)}
      onMouseDown={(e) => e.target instanceof HTMLInputElement || e.preventDefault()} // keep the editor selection
    >
      <div
        className="flex h-9 items-center gap-0.5 rounded-xl border bg-popover p-1 text-xs text-popover-foreground shadow-lg transition-[opacity,scale] duration-150 ease-out starting:scale-[0.97] starting:opacity-0"
        role="toolbar"
        aria-label={t('Quick actions')}
      >
        {running && job ? (
          <>
            <ShimmerText className="px-2 font-normal">{working[job.action]}…</ShimmerText>
            <Button size="icon-xs" variant="ghost" onClick={reset} aria-label={t('Stop')}>
              <Square className="fill-current" />
            </Button>
          </>
        ) : pending ? (
          <>
            <Button size="xs" onClick={() => accept(view)}>
              <Check /> {t('Accept')} <kbd className="opacity-60">Tab</kbd>
            </Button>
            <Button size="xs" variant="ghost" onClick={() => reject(view)}>
              <X /> {t('Reject')} <kbd className="opacity-60">Esc</kbd>
            </Button>
          </>
        ) : error ? (
          <>
            <span
              className={cn(
                'max-w-80 truncate px-2',
                error === noChanges ? 'text-muted-foreground' : 'text-destructive',
              )}
              title={error}
            >
              {error}
            </span>
            {job && (
              <Button
                size="icon-xs"
                variant="ghost"
                onClick={() => run(job.action, job.instruction)}
                aria-label={t('Retry')}
              >
                <RotateCcw />
              </Button>
            )}
            <Button size="icon-xs" variant="ghost" onClick={reset} aria-label={t('Close')}>
              <X />
            </Button>
          </>
        ) : asking ? (
          <form
            className="flex items-center gap-1"
            onSubmit={(e) => {
              e.preventDefault()
              if (instruction.trim()) run('custom')
            }}
          >
            <input
              ref={input}
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  e.preventDefault()
                  setAsking(false)
                  view.focus()
                }
              }}
              placeholder={t('Tell the assistant what to change…')}
              aria-label={t('Instruction')}
              className="h-7 w-72 bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground"
            />
            <Button
              type="submit"
              size="icon-xs"
              className="rounded-full"
              disabled={!instruction.trim()}
              aria-label={t('Apply instruction')}
            >
              <ArrowUp />
            </Button>
          </form>
        ) : (
          <>
            {actions.map((x) => (
              <Button
                key={x.id}
                size="xs"
                variant="ghost"
                className={cn('text-muted-foreground hover:text-foreground', x.id === 'explain' && 'ml-0.5')}
                onClick={() => run(x.id)}
                title={compact ? x.label : undefined}
                aria-label={x.label}
              >
                <x.icon /> {!compact && x.label}
              </Button>
            ))}
            {onCite && (
              <Button
                size="xs"
                variant="ghost"
                className="text-muted-foreground hover:text-foreground"
                onClick={onCite}
                title={compact ? t('Cite') : undefined}
                aria-label={t('Find a citation')}
              >
                <BookMarked /> {!compact && t('Cite')}
              </Button>
            )}
            <span className="mx-0.5 h-4 w-px bg-border" />
            <Button
              title={compact ? t('Ask…') : undefined}
              aria-label={t('Ask…')}
              size="xs"
              variant="ghost"
              className="text-muted-foreground hover:text-foreground"
              onClick={() => {
                setAsking(true)
                requestAnimationFrame(() => input.current?.focus())
              }}
            >
              <Pencil /> {!compact && t('Ask…')}
            </Button>
          </>
        )}
      </div>

      {explanation && (
        <div className="max-h-72 w-[min(28rem,calc(100vw-2rem))] enter overflow-auto rounded-xl border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
          <div className="mb-1 flex items-center justify-between">
            <span className="text-xs font-medium text-muted-foreground">{t('Explanation')}</span>
            <Button size="icon-xs" variant="ghost" onClick={reset} aria-label={t('Close explanation')}>
              <X />
            </Button>
          </div>
          <Markdown text={explanation} />
        </div>
      )}
    </div>
  )
}
