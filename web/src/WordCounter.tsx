import { useEffect, useMemo, useRef, useState } from 'react'
import { EditorView } from '@codemirror/view'
import { Check, Sparkles, X } from 'lucide-react'
import { toast } from 'sonner'
import { api, errorText } from './api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/lib/i18n'
import { latexWords, selectedWords } from '@/lib/wordcount'
import { limitAt, setLimits, wordLimits, type WordLimit } from '@/lib/wordlimits'
import { getPrefs, openSettings } from '@/lib/prefs'
import { accept, proposal, propose, reject } from '@/lib/proposal'

export type LimitTarget = { name: string; from: number; to: number; section?: string; id?: string }

type Props = {
  view: EditorView
  path: string
  ai: boolean
  enabled: boolean
  onCount: (count: number | null) => void
  target: LimitTarget | null
  onClose: () => void
}

export default function WordCounter({ view, path, ai, enabled, onCount, target, onClose }: Props) {
  const source = view.state.doc.toString()
  const limits = view.state.field(wordLimits)
  const { from, to, empty } = view.state.selection.main
  const words = useMemo(() => latexWords(source, limits.rules), [source, limits.rules])
  const count = empty ? null : selectedWords(words, from, to).length
  const [max, setMax] = useState('100')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<{ before: number; after: number; text: string } | null>(null)
  const abort = useRef<AbortController | null>(null)
  const ownedProposal = useRef<ReturnType<typeof proposal>>(null)
  const pending = proposal(view.state)
  const existing =
    target && limits.ranges.find((r) => (target.section ? r.section === target.section : r.id === target.id))

  useEffect(
    () => () => {
      abort.current?.abort()
      if (ownedProposal.current && proposal(view.state) === ownedProposal.current) reject(view)
    },
    [],
  )
  useEffect(() => {
    onCount(enabled ? count : null)
    return () => onCount(null)
  }, [enabled, count, onCount])
  useEffect(() => {
    if (target) setMax(String(existing?.max ?? 100))
  }, [target])

  function save() {
    if (!target || !Number.isSafeInteger(Number(max)) || Number(max) < 1) return
    const range: WordLimit = { ...target, id: existing?.id ?? crypto.randomUUID(), max: Number(max) }
    view.dispatch({
      effects: setLimits.of({ ...limits, ranges: [...limits.ranges.filter((r) => r.id !== range.id), range] }),
    })
    onClose()
    view.focus()
  }

  async function shorten(range: WordLimit) {
    const state = view.state
    const original = state.sliceDoc(range.from, range.to)
    abort.current?.abort()
    const controller = new AbortController()
    abort.current = controller
    setRunning(true)
    setResult(null)
    const before = selectedWords(words, range.from, range.to).length
    try {
      const response = await api<{ text: string }>('/api/quick', {
        method: 'POST',
        signal: controller.signal,
        body: {
          action: 'custom',
          selection: original,
          file: path,
          model: getPrefs().model,
          before: state.sliceDoc(Math.max(0, range.from - 800), range.from),
          after: state.sliceDoc(range.to, Math.min(state.doc.length, range.to + 800)),
          instruction: `Reduce this fragment to at most ${range.max} readable words (aim below the maximum). Preserve all essential ideas, facts, numbers, citations, language and valid LaTeX. Do not invent information. Counting includes these elements only when true: ${JSON.stringify(limits.rules)}. LaTeX commands, comments and mathematical expressions do not count. Return only the replacement fragment.`,
        },
      })
      if (controller.signal.aborted) return
      if (view.state.doc !== state.doc || view.state.field(wordLimits) !== limits)
        throw new Error(t('The text changed while the assistant was working. Try again.'))
      const next = source.slice(0, range.from) + response.text + source.slice(range.to)
      const after = selectedWords(latexWords(next, limits.rules), range.from, range.from + response.text.length).length
      if (!response.text.trim() || after === 0 || after > range.max)
        throw new Error(t('The suggestion does not meet the word limit. Try again.'))
      propose(view, range.from, range.to, response.text)
      ownedProposal.current = proposal(view.state)
      setResult({ before, after, text: response.text })
      view.focus()
    } catch (error) {
      if (!controller.signal.aborted) toast.error(errorText(error))
    } finally {
      if (abort.current === controller) setRunning(false)
    }
  }

  const exceeded = limits.ranges.filter((r) => selectedWords(words, r.from, r.to).length > r.max)
  const contextual = limitAt(source, limits.ranges, from, to)
  const active = exceeded.find((r) => r.id === contextual?.id)
  const reviewing = result && pending?.text === result.text

  return (
    <>
      {(running || reviewing || (active && !pending)) && (
        <div
          className="absolute top-3 right-3 z-40 flex max-w-[calc(100%-1.5rem)] flex-wrap items-center gap-2 rounded-xl border bg-popover p-2 text-xs shadow-lg"
          role="region"
          aria-label={t('Word limit assistant')}
        >
          {running ? (
            <>
              <span role="status" className="px-1">
                {t('Preparing a shorter version…')}
              </span>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => {
                  abort.current?.abort()
                  setRunning(false)
                }}
              >
                {t('Stop')}
              </Button>
            </>
          ) : reviewing ? (
            <>
              <span role="status" className="px-1">
                {t('{before} → {after} words', result)}
              </span>
              <Button
                size="xs"
                variant="secondary"
                onClick={() => {
                  accept(view)
                  setResult(null)
                  view.focus()
                }}
              >
                <Check />
                {t('Accept')}
              </Button>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => {
                  reject(view)
                  setResult(null)
                  view.focus()
                }}
              >
                <X />
                {t('Reject')}
              </Button>
            </>
          ) : (
            <>
              {active && (
                <Button
                  size="sm"
                  variant="ghost"
                  title={ai ? active.name : t('Connect AI in Settings to adapt text.')}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => (ai ? shorten(active) : openSettings('assistant'))}
                >
                  <Sparkles />
                  {t('Suggest a shorter version: {name}', { name: active.name })}
                </Button>
              )}
            </>
          )}
        </div>
      )}
      <Dialog open={!!target} onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle>{t('Word limit')}</DialogTitle>
          <DialogDescription>{target?.name}</DialogDescription>
          <form
            className="grid gap-5"
            onSubmit={(e) => {
              e.preventDefault()
              save()
            }}
          >
            <label className="grid gap-2">
              {t('Maximum words')}
              <Input
                autoFocus
                type="number"
                min="1"
                step="1"
                required
                value={max}
                onChange={(e) => setMax(e.target.value)}
              />
            </label>
            <DialogFooter>
              {existing && (
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    view.dispatch({
                      effects: setLimits.of({ ...limits, ranges: limits.ranges.filter((r) => r.id !== existing.id) }),
                    })
                    onClose()
                    view.focus()
                  }}
                >
                  {t('Remove limit')}
                </Button>
              )}
              <Button type="submit">{t('Set limit')}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
