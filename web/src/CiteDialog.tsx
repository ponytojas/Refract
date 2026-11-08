import { useEffect, useState } from 'react'
import { BookMarked, ExternalLink, Loader2, Quote, Search, Sparkles } from 'lucide-react'
import { toast } from 'sonner'
import { api, errorText } from './api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ShimmerText } from '@/components/ui/chatcn/shimmer-text'
import { t, tn } from '@/lib/i18n'
import { getPrefs } from '@/lib/prefs'

type Candidate = {
  id: string
  title: string
  authors: string[]
  year: number
  venue: string
  abstract: string
  cited_by: number
  url: string
  reason?: string
}
export type Citation = {
  key: string
  path: string
  added: boolean
  needs_setup: boolean
  load: string // the .bib as \bibliography names it, relative to the main file's folder
  text?: string
  placed: boolean
}
// The editor selection the citation is for (null: insert at the cursor).
export type CiteTarget = { path: string; from: number; to: number; text: string; before: string; after: string }

type Props = {
  projectId: string
  open: boolean
  target: CiteTarget | null
  suggest: boolean // look for supporting papers as soon as it opens
  onClose: () => void
  onCited: (c: Citation, target: CiteTarget | null) => Promise<void>
}

const authorsOf = (a: string[]) => (a.length > 3 ? `${a.slice(0, 3).join(', ')} et al.` : a.join(', '))

// Find a real paper (OpenAlex and arXiv) and cite it. With a selection, the
// assistant suggests papers that support it and places \cite in it as a diff.
// Entries are built on the server from the sources' metadata, never by a model.
export default function CiteDialog({ projectId, open, target, suggest, onClose, onCited }: Props) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Candidate[] | null>(null)
  const [busy, setBusy] = useState<'search' | 'suggest' | null>(null)
  const [citing, setCiting] = useState('')

  useEffect(() => {
    if (!open) return
    setQuery('')
    setResults(null)
    if (suggest && target) findSupport()
  }, [open])

  async function load(kind: 'search' | 'suggest', fn: () => Promise<Candidate[]>) {
    setBusy(kind)
    setResults(null)
    try {
      setResults(await fn())
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const findSupport = () =>
    target &&
    load('suggest', () =>
      api<Candidate[]>(`/api/projects/${projectId}/papers/suggest`, {
        method: 'POST',
        body: { text: target.text, model: getPrefs().model },
      }),
    )

  function search(e: React.FormEvent) {
    e.preventDefault()
    if (query.trim()) load('search', () => api<Candidate[]>(`/api/papers/search?q=${encodeURIComponent(query.trim())}`))
  }

  async function cite(c: Candidate) {
    setCiting(c.id)
    try {
      const place = target ? { selection: target.text, before: target.before, after: target.after } : undefined
      const res = await api<Citation>(`/api/projects/${projectId}/cite`, {
        method: 'POST',
        body: { id: c.id, model: getPrefs().model, place },
      })
      await onCited(res, target)
      onClose()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setCiting('')
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[min(680px,calc(100dvh-2rem))] flex-col gap-0 overflow-hidden rounded-2xl p-0 sm:max-w-2xl">
        <DialogTitle className="flex items-center gap-2 px-5 pt-5 text-base">
          <BookMarked className="size-4 text-muted-foreground" /> {t('Add a reference')}
        </DialogTitle>
        <DialogDescription className="px-5 pt-1 text-sm">
          {target
            ? t(
                'Find a paper that supports the selected text, or paste a DOI, arXiv id or URL. The citation is placed in your selection for you to review.',
              )
            : t(
                'Search published papers and arXiv, or paste a DOI, arXiv id or URL. \\cite is inserted at the cursor.',
              )}
        </DialogDescription>

        {target && (
          <div className="mx-5 mt-3 flex items-start gap-3 rounded-lg border bg-foreground/[0.03] p-3">
            <p className="line-clamp-3 min-w-0 flex-1 text-xs text-muted-foreground italic">“{target.text.trim()}”</p>
            <Button size="sm" variant="secondary" className="shrink-0" disabled={!!busy} onClick={findSupport}>
              <Sparkles /> {t('Find supporting papers')}
            </Button>
          </div>
        )}

        <form onSubmit={search} className="flex gap-2 px-5 pt-3 pb-3">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <input
              autoFocus={!suggest}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('Keywords, a title, a DOI, an arXiv id or a URL')}
              aria-label={t('Search papers')}
              className="h-9 w-full rounded-lg border bg-transparent pr-3 pl-9 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            />
          </div>
          <Button type="submit" disabled={!!busy || !query.trim()}>
            {busy === 'search' && <Loader2 className="animate-spin" />} {t('Search')}
          </Button>
        </form>

        <ol className="min-h-0 flex-1 overflow-y-auto border-t px-2 py-2">
          {busy === 'suggest' && (
            <li className="p-4 text-center text-sm">
              <ShimmerText>{t('Looking for papers that support it…')}</ShimmerText>
            </li>
          )}
          {results?.length === 0 && (
            <li className="p-4 text-center text-sm text-muted-foreground">
              {t('No suitable papers found. Try a search with your own keywords.')}
            </li>
          )}
          {!results && !busy && (
            <li className="p-4 text-center text-sm text-muted-foreground">
              {t('Search by keywords, title, author or id.')}
            </li>
          )}
          {results?.map((c) => (
            <li key={c.id} className="group flex enter gap-3 rounded-lg p-3 hover:bg-foreground/[0.03]">
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium">{c.title}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {authorsOf(c.authors)} · {c.year}
                  {c.venue && <> · {c.venue}</>}
                  {c.cited_by > 0 && <> · {tn(c.cited_by, 'cited {n} time', 'cited {n} times')}</>} ·{' '}
                  <a
                    className="inline-flex items-center gap-0.5 hover:text-foreground"
                    href={c.url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {c.id.replace(/^doi:/, 'DOI ').replace(/^arxiv:/, 'arXiv:')} <ExternalLink className="size-3" />
                  </a>
                </p>
                {c.reason && (
                  <p className="mt-1.5 flex gap-1.5 text-xs text-foreground/90">
                    <Sparkles className="mt-0.5 size-3 shrink-0 text-muted-foreground" /> {c.reason}
                  </p>
                )}
                {c.abstract && <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{c.abstract}</p>}
              </div>
              <Button
                size="sm"
                variant="secondary"
                className="shrink-0 self-start"
                disabled={!!citing}
                onClick={() => cite(c)}
              >
                {citing === c.id ? <Loader2 className="animate-spin" /> : <Quote />} {t('Cite')}
              </Button>
            </li>
          ))}
        </ol>
      </DialogContent>
    </Dialog>
  )
}
