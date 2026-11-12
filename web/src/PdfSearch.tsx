import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, ChevronUp, Search, X } from 'lucide-react'
import type { PDFDocumentProxy } from 'pdfjs-dist'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/lib/i18n'
import { mod } from '@/lib/shortcuts'
import { findPdfMatches, indexPdfText, type PdfTextIndex } from '@/lib/pdfsearch'

export function usePdfSearch(doc: PDFDocumentProxy | null, goTo: (page: number) => void, focusViewer: () => void) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<{ doc: PDFDocumentProxy | null; index: number }>({ doc: null, index: 0 })
  const current = selected.doc === doc ? selected.index : 0
  const [indexed, setIndexed] = useState<{ doc: PDFDocumentProxy; pages: PdfTextIndex[] } | null>(null)
  const [failedDoc, setFailedDoc] = useState<PDFDocumentProxy | null>(null)
  const failed = doc !== null && failedDoc === doc && indexed?.doc !== doc
  const busy = open && doc !== null && indexed?.doc !== doc && !failed
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!doc || !open || indexed?.doc === doc) return
    let cancelled = false
    ;(async () => {
      const pages: PdfTextIndex[] = []
      for (let n = 1; n <= doc.numPages; n++) {
        if (cancelled) return
        const content = await (await doc.getPage(n)).getTextContent()
        pages.push(indexPdfText(content.items.filter((item) => 'str' in item)))
      }
      if (!cancelled) setIndexed({ doc, pages })
    })().catch(() => {
      if (!cancelled) setFailedDoc(doc)
    })
    return () => {
      cancelled = true
    }
  }, [doc, open, indexed?.doc])

  const matches = useMemo(
    () => (open && indexed?.doc === doc ? findPdfMatches(indexed.pages, query) : []),
    [open, indexed, doc, query],
  )
  const active = matches[Math.min(current, Math.max(0, matches.length - 1))]
  useEffect(() => {
    if (active) goTo(active.page)
  }, [active, goTo])
  useEffect(() => {
    if (open) input.current?.focus()
  }, [open])

  function show() {
    setOpen(true)
    input.current?.focus()
    input.current?.select()
  }
  function close() {
    setOpen(false)
    setFailedDoc(null)
    focusViewer()
  }
  function step(direction: number) {
    if (matches.length) setSelected({ doc, index: (current + direction + matches.length) % matches.length })
  }

  return {
    show,
    matches,
    active,
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
          if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            close()
          }
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter') {
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
            setSelected({ doc, index: 0 })
            setQuery(e.target.value)
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
