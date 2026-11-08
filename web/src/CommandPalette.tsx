import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { Dialog } from '@base-ui/react/dialog'
import { CornerDownLeft, Search } from 'lucide-react'
import { collect, setOverlay, useOverlay, type Command } from '@/lib/commands'
import { score } from '@/lib/fuzzy'
import { cn } from '@/lib/utils'
import { t } from '@/lib/i18n'

const PER_GROUP = 8 // with an empty query, each group shows its first few
const ORDER = ['Selection', 'Actions', 'Sections', 'Files', 'Chats', 'Projects', 'Navigate', 'General']
const groupRank = (g: string) => (ORDER.includes(g) ? ORDER.indexOf(g) : ORDER.length)
const MAX = 50
// Group names are data (compared against ORDER); only their display is translated.
const GROUP_NAMES: Record<string, string> = {
  Selection: t('Selection'),
  Actions: t('Actions'),
  Sections: t('Sections'),
  Files: t('Files'),
  Chats: t('Chats'),
  Projects: t('Projects'),
  Navigate: t('Navigate'),
  General: t('General'),
}
const groupName = (g: string) => GROUP_NAMES[g] ?? g

// ⌘K. Opened many times a day, so it does not animate (Emil Kowalski).
export default function CommandPalette() {
  const open = useOverlay() === 'palette'
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && setOverlay(null)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/25" />
        <Dialog.Popup
          aria-label={t('Command palette')}
          className="fixed top-[16vh] left-1/2 z-50 flex max-h-[min(30rem,70vh)] w-[min(36rem,calc(100vw-2rem))] -translate-x-1/2 flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl outline-none"
        >
          {open && <Palette />}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function rank(all: Command[], query: string): Command[] {
  const pinned = all.filter((c) => c.pinned)
  const rest = all.filter((c) => !c.pinned).sort((a, b) => groupRank(a.group) - groupRank(b.group)) // stable: providers keep their order
  if (!query.trim()) {
    const seen: Record<string, number> = {}
    return [...pinned, ...rest.filter((c) => (seen[c.group] = (seen[c.group] ?? 0) + 1) <= PER_GROUP)]
  }
  const scored = rest
    .map((c) => ({
      c,
      s: Math.max(score(c.label, query) * 2, score(`${c.label} ${c.hint ?? ''} ${c.keywords ?? ''} ${c.group}`, query)),
    }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s)
  // Letters scattered through a long label are noise next to a real match.
  const best = scored[0]?.s ?? 0
  return [
    ...pinned,
    ...scored
      .filter((x) => x.s >= best * 0.05)
      .slice(0, MAX)
      .map((x) => x.c),
  ]
}

function Palette() {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const items = useMemo(() => rank(collect(query), query), [query])
  const list = useRef<HTMLDivElement>(null)
  const grouped = !query.trim()

  useEffect(() => setActive(0), [query])
  useEffect(() => {
    list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active])

  function run(c: Command | undefined) {
    if (!c) return
    setOverlay(null)
    // After the dialog has closed and handed focus back.
    requestAnimationFrame(() => requestAnimationFrame(c.run))
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const d = e.key === 'ArrowDown' ? 1 : -1
      setActive((a) => (items.length ? (a + d + items.length) % items.length : 0))
    } else if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      e.preventDefault()
      run(items[active])
    }
  }

  return (
    <>
      <div className="flex items-center gap-2 border-b px-3">
        <Search className="size-4 shrink-0 text-muted-foreground" />
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKey}
          placeholder={t('Type a command, a file or a question…')}
          role="combobox"
          aria-expanded
          aria-controls="palette-list"
          aria-activedescendant={items[active] ? `palette-${active}` : undefined}
          className="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        />
      </div>
      <div ref={list} id="palette-list" role="listbox" className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {items.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">{t('No results')}</p>}
        {items.map((c, i) => {
          const heading = grouped && !c.pinned && c.group !== items[i - 1]?.group
          return (
            <div key={c.id}>
              {heading && (
                <div className="px-2 pt-2 pb-1 text-xs font-medium text-muted-foreground">{groupName(c.group)}</div>
              )}
              <div
                id={`palette-${i}`}
                data-index={i}
                role="option"
                aria-selected={i === active}
                onMouseMove={() => i !== active && setActive(i)}
                onClick={() => run(c)}
                className={cn(
                  'flex h-9 cursor-default items-center gap-2.5 rounded-lg px-2 text-sm',
                  i === active && 'bg-accent text-accent-foreground',
                )}
              >
                {c.icon ? (
                  <c.icon className="size-4 shrink-0 text-muted-foreground" />
                ) : (
                  <span className="size-4 shrink-0" />
                )}
                <span className="truncate">{c.label}</span>
                {c.hint && <span className="min-w-0 truncate text-xs text-muted-foreground">{c.hint}</span>}
                <span className="flex-1" />
                {!grouped && !c.pinned && (
                  <span className="shrink-0 text-xs text-muted-foreground">{groupName(c.group)}</span>
                )}
                {c.shortcut && (
                  <kbd className="shrink-0 rounded border bg-muted px-1.5 font-mono text-[11px] text-muted-foreground">
                    {c.shortcut}
                  </kbd>
                )}
                {i === active && <CornerDownLeft className="size-3.5 shrink-0 text-muted-foreground" />}
              </div>
            </div>
          )
        })}
      </div>
      <div className="flex items-center gap-3 border-t px-3 py-2 text-xs text-muted-foreground">
        <span>{t('↑↓ to move')}</span>
        <span>{t('↵ to run')}</span>
        <span>{t('esc to close')}</span>
      </div>
    </>
  )
}
