import { useEffect, useLayoutEffect, useMemo, useReducer, useRef } from 'react'
import { ChevronDown, ChevronUp, Search, X } from 'lucide-react'
import type { PDFDocumentProxy } from 'pdfjs-dist'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/lib/i18n'
import { mod } from '@/lib/shortcuts'
import { groupPdfMatches, indexPdfPages } from '@/lib/pdfsearch'
import { initialPdfSearch, pdfSearchMatches, pdfSearchReducer, pdfScrollRequest } from '@/lib/pdfsearch-state'

export function usePdfSearch(doc: PDFDocumentProxy | null, goTo: (page: number) => void, focusViewer: () => void) {
  const [state, dispatch] = useReducer(pdfSearchReducer, initialPdfSearch)
  const { open, query, current, indexed, failedDoc } = state
  const failed = doc !== null && failedDoc === doc && indexed?.doc !== doc
  const busy = open && doc !== null && indexed?.doc !== doc && !failed
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!doc || !open || indexed?.doc === doc) return
    let cancelled = false
    ;(async () => {
      const pages = await indexPdfPages(
        doc.numPages,
        async (n) => {
          const content = await (await doc.getPage(n)).getTextContent()
          return content.items.filter((item) => 'str' in item)
        },
        () => cancelled,
      )
      if (pages && !cancelled) dispatch({ type: 'loaded', doc, pages })
    })().catch(() => {
      if (!cancelled) dispatch({ type: 'failed', doc })
    })
    return () => {
      cancelled = true
    }
  }, [doc, open, indexed?.doc])

  const matches = pdfSearchMatches(state, doc)
  const byPage = useMemo(() => groupPdfMatches(matches), [matches])
  const active = matches[current]
  const scrollRequest = useMemo(() => pdfScrollRequest(state, doc), [state, doc])
  useLayoutEffect(() => {
    if (scrollRequest) goTo(scrollRequest.match.page)
  }, [scrollRequest, goTo])
  useEffect(() => {
    if (open) input.current?.focus()
  }, [open])

  function show() {
    if (!doc) return
    dispatch({ type: 'show', doc })
    input.current?.focus()
    input.current?.select()
  }
  function close() {
    dispatch({ type: 'close' })
    focusViewer()
  }
  function step(direction: number) {
    if (doc && matches.length)
      dispatch({ type: 'select', doc, index: (current + direction + matches.length) % matches.length })
  }

  return {
    show,
    matches,
    byPage,
    active,
    scrollRequest,
    button: (
      <Button
        variant="ghost"
        size="icon-sm"
        disabled={!doc}
        aria-label={t('Search PDF')}
        title={`${t('Search PDF')} (${mod}F)`}
        onClick={show}
      >
        <Search />
      </Button>
    ),
    bar: open && (
      <div
        role="search"
        aria-label={t('Search PDF')}
        className="flex shrink-0 items-center gap-1 border-b px-2 py-1.5"
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing) return
          if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            close()
          }
          if (e.target === input.current && (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter')) {
            e.preventDefault()
            e.stopPropagation()
            step(e.key === 'ArrowUp' || (e.key === 'Enter' && e.shiftKey) ? -1 : 1)
          }
        }}
      >
        <Input
          ref={input}
          value={query}
          maxLength={200}
          aria-label={t('Find in PDF')}
          placeholder={t('Find in PDF')}
          onChange={(e) => {
            dispatch({ type: 'query', doc, query: e.target.value })
          }}
          className="h-8"
        />
        <span role="status" className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {busy
            ? t('Searching…')
            : failed
              ? t('Could not search this PDF.')
              : !query.trim()
                ? ''
                : matches.length
                  ? t('{current} of {total}', { current: Math.min(current + 1, matches.length), total: matches.length })
                  : t('No matches')}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={!matches.length}
          aria-label={t('Previous match')}
          onClick={() => step(-1)}
        >
          <ChevronUp />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={!matches.length}
          aria-label={t('Next match')}
          onClick={() => step(1)}
        >
          <ChevronDown />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label={t('Close PDF search')} onClick={close}>
          <X />
        </Button>
      </div>
    ),
  }
}
