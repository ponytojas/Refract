import { useEffect, useState } from 'react'
import { AlertTriangle, CheckCircle2, ChevronRight, CircleX, FileWarning, Loader2, ScrollText, Sparkles } from 'lucide-react'
import { api, type Warning } from './api'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { t, tn } from '@/lib/i18n'

export type Problem = { severity: 'error' | 'warning'; kind: string; file?: string; line?: number; message: string }
export type CompileResult = {
  ok: boolean
  problems: Problem[]
  output: string
  timed_out: boolean
  superseded: boolean
  duration_ms: number
  pdf?: string
  checkpoint: boolean
}

type Props = {
  projectId: string
  compiling: boolean
  result: CompileResult | null
  error: string
  onJump: (file: string, line: number) => void
  onFix?: (problems: Problem[]) => void // ask the assistant; absent when AI is unavailable
}

// Noisy kinds, collapsed by default.
const groups = [
  { kinds: ['overfull', 'underfull'], label: (n: number) => tn(n, '{n} overfull/underfull box', '{n} overfull/underfull boxes') },
  { kinds: ['absolute-path'], label: (n: number) => tn(n, '{n} file read from outside the project', '{n} files read from outside the project') },
]

// The compile state as a chip; click for problems and the log.
export default function CompileStatus({ projectId, compiling, result, error, onJump, onFix }: Props) {
  const [checks, setChecks] = useState<Warning[]>([])
  const [open, setOpen] = useState(false)
  const [expanded, setExpanded] = useState<number[]>([])
  const [log, setLog] = useState<string | null>(null)

  // Portability checks follow the sources: refresh after every compile.
  useEffect(() => {
    api<Warning[]>(`/api/projects/${projectId}/checks`)
      .then(setChecks)
      .catch(() => {})
  }, [projectId, result])

  const problems = result?.problems ?? []
  const errors = problems.filter((x) => x.severity === 'error').length
  const warnings = problems.length - errors + checks.length
  // Errors first: they stop the build; warnings keep the log's order.
  const main = problems.filter((x) => !groups.some((g) => g.kinds.includes(x.kind))).sort((a, b) => Number(b.severity === 'error') - Number(a.severity === 'error'))
  const portability: Problem[] = checks.map((w) => ({ severity: 'warning', kind: 'portability', file: w.file, line: w.line, message: w.message }))
  const fixable = [...main, ...portability] // the noisy groups (box warnings) are left out of "fix all"
  const fix = onFix
    ? (ps: Problem[]) => {
        setOpen(false)
        onFix(ps)
      }
    : undefined

  let icon = <CheckCircle2 className="text-success" />
  let label = t('Compile to preview')
  if (compiling) {
    icon = <Loader2 className="animate-spin text-muted-foreground" />
    label = t('Compiling…')
  } else if (error) {
    icon = <CircleX className="text-destructive" />
    label = t('Could not compile')
  } else if (result?.superseded) {
    label = t('Cancelled')
  } else if (result && errors > 0) {
    icon = <CircleX className="text-destructive" />
    label = tn(errors, '{n} error', '{n} errors')
  } else if (result) {
    label = t('Compiled · {seconds}s', { seconds: (result.duration_ms / 1000).toFixed(1) })
    if (warnings > 0) icon = <AlertTriangle className="text-warning" />
  } else if (!result && checks.length) {
    icon = <AlertTriangle className="text-warning" />
  }

  async function showLog() {
    const res = await fetch(`/api/projects/${projectId}/log`, { headers: { 'X-Refract': '1' } })
    setLog(res.ok ? await res.text() : t('No log yet: compile first.'))
  }

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          render={
            <Button
              variant="ghost"
              size="sm"
              className="min-w-0 shrink gap-1.5 rounded-full px-2.5 font-normal"
              aria-label={t('Compile status and problems')}
            />
          }
        >
          {/* key: a soft crossfade when the state changes */}
          <span key={label} className="enter flex min-w-0 items-center gap-1.5 [&_svg]:size-4">
            {icon}
            <span className="truncate tabular-nums">{label}</span>
          </span>
          {warnings > 0 && !compiling && errors === 0 && result && (
            <span className="text-xs text-muted-foreground">· {tn(warnings, '{n} warning', '{n} warnings')}</span>
          )}
        </PopoverTrigger>
        <PopoverContent align="start" className="max-h-[60vh] w-[26rem] overflow-auto p-0">
          <div className="flex items-center justify-between border-b px-3 py-2">
            <span className="text-sm font-medium">{t('Problems')}</span>
            <span className="flex items-center gap-1">
              {fix && fixable.length > 0 && (
                <Button variant="secondary" size="xs" onClick={() => fix(fixable)}>
                  <Sparkles /> {t('Fix all with AI')}
                </Button>
              )}
              <Button variant="ghost" size="xs" onClick={showLog}>
                <ScrollText /> {t('Full log')}
              </Button>
            </span>
          </div>
          {error && <p className="px-3 py-2 text-sm text-destructive">{error}</p>}
          <ul className="divide-y">
            <ProblemRows problems={main} onJump={(f, l) => (setOpen(false), onJump(f, l))} onFix={fix} />
            {groups.map((g, gi) => {
              const items = problems.filter((x) => g.kinds.includes(x.kind))
              if (!items.length) return null
              const isOpen = expanded.includes(gi)
              return (
                <li key={'g' + gi}>
                  <button
                    className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-xs text-muted-foreground transition-colors duration-100 hover:text-foreground"
                    onClick={() => setExpanded(isOpen ? expanded.filter((x) => x !== gi) : [...expanded, gi])}
                  >
                    <ChevronRight className={cn('size-3.5 transition-transform duration-150 ease-out', isOpen && 'rotate-90')} /> {g.label(items.length)}
                  </button>
                  {isOpen && (
                    <ul className="divide-y border-t">
                      <ProblemRows problems={items} onJump={(f, l) => (setOpen(false), onJump(f, l))} onFix={fix} />
                    </ul>
                  )}
                </li>
              )
            })}
            <ProblemRows problems={portability} onJump={(f, l) => (setOpen(false), onJump(f, l))} onFix={fix} />
          </ul>
          {!error && problems.length === 0 && checks.length === 0 && (
            <p className="px-3 py-6 text-center text-sm text-muted-foreground">{result ? t('No problems.') : t('Compile to check the document.')}</p>
          )}
        </PopoverContent>
      </Popover>

      <Dialog open={log !== null} onOpenChange={(o) => !o && setLog(null)}>
        <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t('Compile log')}</DialogTitle>
          </DialogHeader>
          <pre className="min-h-0 flex-1 overflow-auto rounded-md bg-muted p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">{log}</pre>
        </DialogContent>
      </Dialog>
    </>
  )
}

// Rows render in pages: a log with hundreds of problems would otherwise build
// them all at once and freeze the page when the list opens.
const PAGE = 50

type RowsProps = { problems: Problem[]; onJump: (file: string, line: number) => void; onFix?: (ps: Problem[]) => void }

function ProblemRows({ problems, onJump, onFix }: RowsProps) {
  const [shown, setShown] = useState(PAGE)
  const rest = problems.length - shown
  const next = Math.min(rest, 2 * PAGE)
  return (
    <>
      {problems.slice(0, shown).map((x, i) => (
        <ProblemRow key={i} problem={x} onJump={onJump} onFix={onFix} />
      ))}
      {rest > 0 && (
        <li>
          <button
            className="w-full px-3 py-2 text-left text-xs text-muted-foreground transition-colors duration-100 hover:text-foreground"
            onClick={() => setShown(shown + next)}
          >
            {tn(next, 'Show {n} more', 'Show {n} more')} · {tn(rest, '{n} left', '{n} left')}
          </button>
        </li>
      )}
    </>
  )
}

function ProblemRow({ problem: x, onJump, onFix }: { problem: Problem; onJump: (file: string, line: number) => void; onFix?: (ps: Problem[]) => void }) {
  const Icon = x.severity === 'error' ? CircleX : x.kind === 'portability' ? FileWarning : AlertTriangle
  const body = (
    <>
      <Icon className={cn('mt-0.5 size-4 shrink-0', x.severity === 'error' ? 'text-destructive' : 'text-warning')} />
      <span className="min-w-0">
        {x.file && (
          <span className="block font-mono text-xs text-muted-foreground">
            {x.file}
            {x.line ? `:${x.line}` : ''}
            {x.kind === 'portability' && ` · ${t('portability')}`}
          </span>
        )}
        <span className="block text-sm leading-snug">{x.message}</span>
      </span>
    </>
  )
  return (
    <li className="group/problem flex items-start transition-colors duration-100 hover:bg-accent/60">
      {x.file ? (
        <button className="flex min-w-0 flex-1 gap-2.5 py-2.5 pl-3 text-left" onClick={() => onJump(x.file!, x.line ?? 1)}>
          {body}
        </button>
      ) : (
        <div className="flex min-w-0 flex-1 gap-2.5 py-2.5 pl-3">{body}</div>
      )}
      {onFix && (
        // A plain button: rows are many, and this one only shows on hover.
        <button
          className="m-2 flex h-6 shrink-0 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground opacity-0 transition-opacity duration-150 group-hover/problem:opacity-100 hover:bg-accent hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 [&_svg]:size-3"
          onClick={() => onFix([x])}
        >
          <Sparkles /> {t('Fix')}
        </button>
      )}
    </li>
  )
}
