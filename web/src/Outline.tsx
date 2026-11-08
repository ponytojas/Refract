import { useEffect, useRef } from 'react'
import { t } from '@/lib/i18n'
import { depths, type Outline as OutlineData } from '@/lib/outline'
import { cn } from '@/lib/utils'

const indent = (depth: number) => 8 + depth * 14 // px

// The sidebar's outline: headings of the main file and the files it includes.
export default function Outline({
  outline,
  current,
  mainFile,
  onJump,
}: {
  outline: OutlineData
  current: number // index of the heading containing the cursor, -1 for none
  mainFile?: string
  onJump: (path: string, line: number) => void
}) {
  const list = useRef<HTMLElement>(null)
  useEffect(() => {
    list.current?.querySelector('[aria-current="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [current])

  if (!mainFile)
    return (
      <p id="outline-list" className="flex-1 px-4 py-2 text-xs text-muted-foreground">
        {t('Set a main file to see its outline.')}
      </p>
    )
  const { headings } = outline
  if (!headings.length)
    return (
      <p id="outline-list" className="flex-1 px-4 py-2 text-xs text-muted-foreground">
        {t('No sections yet.')}
      </p>
    )
  const depth = depths(headings)
  return (
    <nav ref={list} id="outline-list" className="min-h-0 flex-1 overflow-auto px-2 pb-2" aria-label={t('Outline')}>
      {headings.map((h, i) => (
        <button
          key={`${h.path}:${h.line}:${i}`}
          aria-current={i === current}
          // The name is the heading alone; the title (file and line) becomes its description.
          aria-label={h.title}
          title={h.path === mainFile ? `${h.title} · L${h.line}` : `${h.title} · ${h.path}:${h.line}`}
          className={cn(
            'relative flex w-full items-center gap-2 rounded-md py-1 pr-2 text-left text-sm transition-colors duration-100',
            i === current ? 'bg-foreground/[0.07] text-foreground' : 'text-foreground/80 hover:bg-foreground/[0.04]',
            depth[i] === 0 && 'font-medium',
          )}
          style={{ paddingLeft: indent(depth[i]) }}
          onClick={() => onJump(h.path, h.line)}
        >
          {/* One guide per enclosing heading, under its title: rows touch, so they read as lines. */}
          {Array.from({ length: depth[i] }, (_, k) => (
            <span
              key={k}
              className="absolute inset-y-0 w-px bg-foreground/10"
              style={{ left: indent(k) + 4 }}
              aria-hidden
            />
          ))}
          <span className="min-w-0 flex-1 truncate">{h.title}</span>
          {h.path !== mainFile && (
            <span className="max-w-[40%] shrink-0 truncate text-xs text-muted-foreground" aria-hidden>
              {h.path.split('/').pop()}
            </span>
          )}
        </button>
      ))}
    </nav>
  )
}
