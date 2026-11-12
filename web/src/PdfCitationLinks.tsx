import { useEffect, useMemo, useRef, useState } from 'react'
import type { PDFDocumentProxy } from 'pdfjs-dist'
import { CornerUpLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { indexPdfCitations, type PdfBacklink, type PdfCitation } from '@/lib/pdflinks'
import { t } from '@/lib/i18n'

export function usePdfCitationLinks(doc: PDFDocumentProxy | null) {
  const [indexed, setIndexed] = useState<{ doc: PDFDocumentProxy; entries: PdfBacklink[] } | null>(null)
  useEffect(() => {
    if (!doc) return
    let cancelled = false
    indexPdfCitations(doc, () => cancelled)
      .then((entries) => {
        if (entries && !cancelled) setIndexed({ doc, entries })
      })
      .catch((error) => {
        if (!cancelled) console.error('PDF citation links:', error)
      })
    return () => {
      cancelled = true
    }
  }, [doc])
  return useMemo(() => {
    const pages = new Map<number, PdfBacklink[]>()
    if (indexed?.doc !== doc) return pages
    for (const entry of indexed.entries) {
      const group = pages.get(entry.target.page)
      if (group) group.push(entry)
      else pages.set(entry.target.page, [entry])
    }
    return pages
  }, [indexed, doc])
}

export default function PdfCitationLinks({
  entries,
  scale,
  onSelect,
}: {
  entries?: PdfBacklink[]
  scale: number
  onSelect: (citation: PdfCitation) => void
}) {
  const selected = useRef(false)
  const controlSize = Math.max(14, Math.min(24, 16 * scale))
  return entries?.map((entry) => {
    const label =
      entry.citations.length === 1
        ? t('Return to citation on page {page}', { page: entry.citations[0].page })
        : t('Show {n} citations of this reference', { n: entry.citations.length })
    const button = (
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        className="border bg-white/95 p-0 text-slate-600 shadow-sm hover:bg-sky-100 hover:text-slate-900"
        style={{ width: controlSize, height: controlSize }}
        aria-label={label}
        title={label}
        data-pdf-backlink={entry.destination}
        onClick={entry.citations.length === 1 ? () => onSelect(entry.citations[0]) : undefined}
      >
        <CornerUpLeft style={{ width: controlSize - 4, height: controlSize - 4 }} />
      </Button>
    )
    return (
      <div
        key={entry.destination}
        className="absolute z-20"
        style={{ left: Math.max(2, entry.target.left * scale - controlSize - 4), top: entry.target.top * scale }}
        onClick={(e) => e.stopPropagation()}
      >
        {entry.citations.length === 1 ? (
          button
        ) : (
          <DropdownMenu
            onOpenChange={(open) => {
              if (open) selected.current = false
            }}
          >
            <DropdownMenuTrigger render={button} />
            <DropdownMenuContent className="w-auto min-w-48" finalFocus={() => !selected.current}>
              {entry.citations.map((citation, index) => (
                <DropdownMenuItem
                  key={index}
                  className="whitespace-nowrap"
                  onClick={() => {
                    selected.current = true
                    onSelect(citation)
                  }}
                >
                  {t('Citation {n} · page {page}', { n: index + 1, page: citation.page })}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    )
  })
}
