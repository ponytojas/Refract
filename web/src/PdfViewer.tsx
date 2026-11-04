import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Download,
  FileCode,
  Maximize2,
  Minus,
  Plus,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { toast } from 'sonner'
import { download, errorText } from './api'
import { WithContextMenu, type MenuEntry } from '@/components/Menu'
import { mod } from '@/lib/shortcuts'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import { plainWords } from '@/lib/wordcount'
import { t } from '@/lib/i18n'
import * as pdfjs from 'pdfjs-dist'
import type { PDFDocumentLoadingTask, PDFDocumentProxy } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'

pdfjs.GlobalWorkerOptions.workerSrc = workerUrl

export type Highlight = { page: number; x: number; y: number; w: number; h: number; n: number }

type Props = {
  projectId: string
  countEnabled: boolean
  onWordCount: (count: number | null) => void
  version: number // bump to reload the PDF after a compile
  highlight?: Highlight
  onInverse: (page: number, x: number, y: number) => void
  toolbar?: ReactNode // left side of the toolbar (compile status)
  actions?: ReactNode // right side, after zoom
  toolbarTarget?: HTMLElement | null // render the toolbar there (shared header row) instead of its own row
}

type Size = { w: number; h: number } // page size in PDF points

// Continuous-scroll viewer. Pages are laid out at their final size up front
// (so scroll position survives a reload) and rendered when they come into view.
export default function PdfViewer({
  countEnabled,
  onWordCount,
  projectId,
  version,
  highlight,
  onInverse,
  toolbar,
  actions,
  toolbarTarget,
}: Props) {
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [sizes, setSizes] = useState<Size[]>([])
  const [scale, setScale] = useState<number | 'fit'>('fit')
  const [current, setCurrent] = useState(1)
  const [selection, setSelection] = useState('')
  const [message, setMessage] = useState('')
  const scroller = useRef<HTMLDivElement>(null)
  const keep = useRef<{ top: number; left: number } | null>(null)
  const zoomAnchor = useRef<number | null>(null) // relative scroll to keep across zoom changes
  const task = useRef<PDFDocumentLoadingTask | null>(null) // owns the shown document

  useEffect(() => {
    const update = () => {
      const selection = window.getSelection()
      if (
        selection &&
        scroller.current?.contains(selection.anchorNode) &&
        scroller.current.contains(selection.focusNode)
      ) {
        setSelection(selection.toString())
      } else {
        setSelection('')
      }
    }
    document.addEventListener('selectionchange', update)
    return () => document.removeEventListener('selectionchange', update)
  }, [])

  useEffect(() => {
    onWordCount(countEnabled && selection.trim() ? plainWords(selection).length : null)
    return () => onWordCount(null)
  }, [countEnabled, selection, onWordCount])

  const fitScale = () => {
    const el = scroller.current
    // A hidden tab has no width: keep a sane scale until it is shown again.
    return el && sizes[0] && el.clientWidth > 64 ? (el.clientWidth - 32) / sizes[0].w : 1
  }
  const s = scale === 'fit' ? fitScale() : scale

  // Load (or reload) the PDF.
  useEffect(() => {
    let cancelled = false
    setSelection('')
    ;(async () => {
      const res = await fetch(`/api/projects/${projectId}/pdf`, { headers: { 'X-Refract': '1' } })
      if (!res.ok) {
        setMessage(
          res.status === 404
            ? t('No PDF yet. Compile to see it here.')
            : t('Could not load the PDF (HTTP {status}).', { status: res.status }),
        )
        return
      }
      const data = new Uint8Array(await res.arrayBuffer())
      const loading = pdfjs.getDocument({ data })
      const loaded = await loading.promise
      const next: Size[] = []
      for (let i = 1; i <= loaded.numPages; i++) {
        const vp = (await loaded.getPage(i)).getViewport({ scale: 1 })
        next.push({ w: vp.width, h: vp.height })
      }
      if (cancelled) return loading.destroy()
      const el = scroller.current
      if (el) keep.current = { top: el.scrollTop, left: el.scrollLeft }
      setMessage('')
      setSizes(next)
      task.current?.destroy() // free the previous document
      task.current = loading
      setDoc(loaded)
    })().catch((e) => !cancelled && setMessage(t('Could not load the PDF: {error}', { error: String(e) })))
    return () => {
      cancelled = true
    }
  }, [projectId, version])

  useEffect(() => () => void task.current?.destroy(), [])

  // Restore the scroll position after a reload, once the new layout exists.
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && keep.current) {
      el.scrollTop = keep.current.top
      el.scrollLeft = keep.current.left
      keep.current = null
    }
  }, [doc])

  // Keep the same relative position when zooming.
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && zoomAnchor.current !== null) {
      el.scrollTop = zoomAnchor.current * el.scrollHeight
      zoomAnchor.current = null
    }
  }, [s])

  // Re-fit on resize.
  const [, force] = useState(0)
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const ro = new ResizeObserver(() => scale === 'fit' && force((n) => n + 1))
    ro.observe(el)
    return () => ro.disconnect()
  }, [scale])

  // Scroll to and flash a forward-sync highlight.
  useEffect(() => {
    const el = scroller.current
    const page = highlight && el?.querySelector<HTMLElement>(`[data-page="${highlight.page}"]`)
    if (!highlight || !el || !page) return
    // Instant, not smooth: a PDF reload landing mid-animation would otherwise
    // restore a half-way position.
    el.scrollTop = page.offsetTop + highlight.y * s - el.clientHeight / 3
  }, [highlight])

  function zoom(next: number | 'fit') {
    const el = scroller.current
    if (el) zoomAnchor.current = el.scrollTop / Math.max(el.scrollHeight, 1)
    setScale(next)
  }

  function onScroll() {
    const el = scroller.current
    if (!el) return
    const mid = el.scrollTop + el.clientHeight / 2
    for (const p of el.querySelectorAll<HTMLElement>('[data-page]')) {
      if (p.offsetTop + p.offsetHeight > mid) {
        setCurrent(Number(p.dataset.page))
        break
      }
    }
  }

  function goTo(n: number) {
    const p = scroller.current?.querySelector<HTMLElement>(`[data-page="${n}"]`)
    if (p && scroller.current) scroller.current.scrollTop = p.offsetTop - 8
  }

  // Right click: where on which page, for "Go to source here".
  const point = useRef<{ page: number; x: number; y: number } | null>(null)
  function rememberPoint(e: MouseEvent) {
    const page = (e.target as HTMLElement).closest<HTMLElement>('[data-page]')
    if (!page) return (point.current = null)
    const r = page.getBoundingClientRect()
    point.current = { page: Number(page.dataset.page), x: (e.clientX - r.left) / s, y: (e.clientY - r.top) / s }
  }
  const menu = (): MenuEntry[] => {
    const p = point.current
    return [
      {
        label: t('Go to source here'),
        icon: FileCode,
        shortcut: `${mod}+click`,
        disabled: !p,
        onSelect: () => p && onInverse(p.page, p.x, p.y),
      },
      '-',
      { label: t('Zoom in'), icon: ZoomIn, disabled: !doc, onSelect: () => zoom(Math.min(4, s * 1.25)) },
      { label: t('Zoom out'), icon: ZoomOut, disabled: !doc, onSelect: () => zoom(Math.max(0.25, s / 1.25)) },
      { label: t('Zoom to fit'), icon: Maximize2, disabled: !doc, onSelect: () => zoom('fit') },
      '-',
      {
        label: t('Download PDF'),
        icon: Download,
        disabled: !doc,
        onSelect: () =>
          download(`/api/projects/${projectId}/pdf`, 'document.pdf').catch((err) => toast.error(errorText(err))),
      },
    ]
  }

  const zoomLabel = scale === 'fit' ? t('Fit') : `${Math.round(s * 100)}%`
  const bar = (
    <>
      {toolbar}
      <span className="flex-1" />
      <DropdownMenu>
        <DropdownMenuTrigger
          render={<Button variant="ghost" size="sm" className="gap-1 font-normal tabular-nums" disabled={!doc} />}
        >
          <Maximize2 /> {zoomLabel} <ChevronDown className="opacity-60" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-40">
          <DropdownMenuItem onClick={() => zoom('fit')}>
            {t('Zoom to fit')} <Check className={cn('ml-auto', scale !== 'fit' && 'invisible')} />
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {[0.5, 0.75, 1, 1.25, 1.5, 2].map((z) => (
            <DropdownMenuItem key={z} onClick={() => zoom(z)}>
              {z * 100}% <Check className={cn('ml-auto', scale !== z && 'invisible')} />
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {actions}
    </>
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {toolbarTarget ? (
        createPortal(<>{bar}</>, toolbarTarget)
      ) : (
        <div className="flex h-11 shrink-0 items-center gap-1 border-b px-2">{bar}</div>
      )}
      <div className="relative min-h-0 flex-1">
        <WithContextMenu items={menu} className="absolute inset-0">
          <div
            className="absolute inset-0 overflow-auto bg-pdf p-4"
            ref={scroller}
            onScroll={onScroll}
            onContextMenu={rememberPoint}
          >
            {message && <p className="mt-24 enter text-center text-sm text-muted-foreground">{message}</p>}
            {doc &&
              sizes.map((size, i) => (
                <Page
                  key={i}
                  doc={doc}
                  n={i + 1}
                  size={size}
                  scale={s}
                  highlight={highlight?.page === i + 1 ? highlight : undefined}
                  onInverse={onInverse}
                />
              ))}
          </div>
        </WithContextMenu>
        {doc && sizes.length > 0 && (
          // Floating pager: page navigation without leaving the document.
          <div className="absolute bottom-4 left-1/2 flex -translate-x-1/2 enter items-center gap-0.5 rounded-full border bg-popover/90 p-1 text-sm whitespace-nowrap shadow-lg backdrop-blur">
            <Button
              variant="ghost"
              size="icon-sm"
              className="rounded-full"
              onClick={() => goTo(current - 1)}
              disabled={current <= 1}
              aria-label={t('Previous page')}
            >
              <ChevronLeft />
            </Button>
            <input
              className="w-8 rounded-md bg-transparent text-center tabular-nums outline-none focus-visible:bg-accent"
              aria-label={t('Page')}
              value={current}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (n >= 1 && n <= sizes.length) goTo(n)
              }}
            />
            <span className="pr-1 whitespace-nowrap text-muted-foreground tabular-nums">/ {sizes.length}</span>
            <Button
              variant="ghost"
              size="icon-sm"
              className="rounded-full"
              onClick={() => goTo(current + 1)}
              disabled={current >= sizes.length}
              aria-label={t('Next page')}
            >
              <ChevronRight />
            </Button>
            <span className="mx-0.5 h-4 w-px bg-border" />
            <Button
              variant="ghost"
              size="icon-sm"
              className="rounded-full"
              onClick={() => zoom(Math.max(0.25, s / 1.2))}
              aria-label={t('Zoom out')}
            >
              <Minus />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              className="rounded-full"
              onClick={() => zoom(Math.min(5, s * 1.2))}
              aria-label={t('Zoom in')}
            >
              <Plus />
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

function Page({
  doc,
  n,
  size,
  scale,
  highlight,
  onInverse,
}: {
  doc: PDFDocumentProxy
  n: number
  size: Size
  scale: number
  highlight?: Highlight
  onInverse: Props['onInverse']
}) {
  const box = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const textHost = useRef<HTMLDivElement>(null)
  const [visible, setVisible] = useState(false)
  const [flash, setFlash] = useState<Highlight | undefined>()

  useEffect(() => {
    const io = new IntersectionObserver(([e]) => setVisible(e.isIntersecting), { rootMargin: '600px 0px' })
    io.observe(box.current!)
    return () => io.disconnect()
  }, [])

  useEffect(() => {
    if (!visible) return
    let cancelled = false
    let task: { cancel: () => void } | null = null
    ;(async () => {
      const page = await doc.getPage(n)
      if (cancelled || !canvas.current) return
      const dpr = window.devicePixelRatio || 1
      const vp = page.getViewport({ scale: scale * dpr })
      // Render off-screen, then swap: no blank flash on recompile or zoom.
      const off = document.createElement('canvas')
      off.width = Math.floor(vp.width)
      off.height = Math.floor(vp.height)
      const t = page.render({ canvas: off, viewport: vp })
      task = t
      await t.promise
      if (cancelled || !canvas.current) return
      canvas.current.width = off.width
      canvas.current.height = off.height
      canvas.current.getContext('2d')!.drawImage(off, 0, 0)
    })().catch(() => {}) // cancelled renders reject
    return () => {
      cancelled = true
      task?.cancel()
    }
  }, [doc, n, scale, visible])

  useEffect(() => {
    if (!visible) return
    let cancelled = false
    let layer: InstanceType<typeof pdfjs.TextLayer> | null = null
    const host = textHost.current!
    host.replaceChildren()
    ;(async () => {
      const page = await doc.getPage(n)
      const content = await page.getTextContent()
      if (cancelled) return
      const container = document.createElement('div')
      container.className = 'textLayer refract-pdf-text'
      container.style.setProperty('--total-scale-factor', String(scale))
      host.append(container)
      layer = new pdfjs.TextLayer({ textContentSource: content, container, viewport: page.getViewport({ scale }) })
      await layer.render()
    })().catch((error) => {
      if (!cancelled) console.error('PDF text layer:', error)
    })
    return () => {
      cancelled = true
      layer?.cancel()
      host.replaceChildren()
    }
  }, [doc, n, scale, visible])

  useEffect(() => {
    if (!highlight) return
    setFlash(highlight)
    const t = setTimeout(() => setFlash(undefined), 2000)
    return () => clearTimeout(t)
  }, [highlight])

  function onClick(e: MouseEvent) {
    if (!(e.metaKey || e.ctrlKey)) return
    const r = box.current!.getBoundingClientRect()
    onInverse(n, (e.clientX - r.left) / scale, (e.clientY - r.top) / scale)
  }

  return (
    <div
      className="relative mx-auto mb-4 bg-white shadow-[0_1px_3px_rgb(0_0_0/0.12),0_0_0_1px_rgb(0_0_0/0.04)]"
      data-page={n}
      ref={box}
      style={{ width: size.w * scale, height: size.h * scale }}
      onClick={onClick}
      title={t('Cmd/Ctrl+click: go to source')}
    >
      <canvas ref={canvas} className="block size-full" />
      <div ref={textHost} className="absolute inset-0 select-text" />
      {flash && (
        <div
          className="sync-highlight"
          style={{
            left: flash.x * scale - 2,
            top: flash.y * scale - 2,
            width: flash.w * scale + 4,
            height: flash.h * scale + 4,
          }}
        />
      )}
    </div>
  )
}
