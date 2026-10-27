import { useEffect, useSyncExternalStore, type ComponentProps } from 'react'
import { flushSync } from 'react-dom'

// A small router for Refract's few pages ("/" and "/p/:id"): history API
// navigation without reloads, wrapped in a view transition where the browser
// has them. Pages take part in two ways: work that must finish before they are
// left (useBeforeLeave: saving edits) and a signal that their content is on
// screen (pageReady), so the transition ends on it rather than on a skeleton.

let current = location.pathname
let previous = ''
let index = (history.state as { i?: number } | null)?.i ?? 0
if (history.state?.i === undefined) history.replaceState({ ...history.state, i: index }, '')

const listeners = new Set<() => void>()
const leaving = new Set<() => Promise<void>>()
let ready: (() => void) | null = null

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

/** The page being shown. It changes once the page being left has finished its work. */
export function usePath(): string {
  return useSyncExternalStore(subscribe, () => current)
}

/** The page shown before this one ("" on first load). */
export const previousPath = () => previous

/** Runs fn (and waits for it) before the page changes. */
export function useBeforeLeave(fn: () => Promise<void>) {
  useEffect(() => {
    leaving.add(fn)
    return () => void leaving.delete(fn)
  }, [fn])
}

/** A page calls this once its content is on screen. */
export function pageReady() {
  ready?.()
  ready = null
}

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))
const reducedMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches

async function show(to: string, back: boolean, animate: boolean) {
  // Saving edits must not be skipped, but a hung request must not trap the user.
  await Promise.race([Promise.all([...leaving].map((f) => f().catch(() => {}))), wait(5000)])
  const update = () => {
    previous = current
    current = to
    listeners.forEach((l) => l())
  }
  if (!animate || !document.startViewTransition || reducedMotion()) return update()
  const root = document.documentElement
  root.dataset.nav = back ? 'back' : 'forward'
  const shown = new Promise<void>((r) => (ready = r))
  const vt = document.startViewTransition(async () => {
    flushSync(update)
    await Promise.race([shown, wait(400)]) // the new page's data, or move on
  })
  vt.finished.finally(() => delete root.dataset.nav)
}

/** Goes to a page of the app. replace: no new history entry, and no animation. */
export function navigate(to: string, { replace = false } = {}) {
  if (to === location.pathname && to === current) return
  if (replace) history.replaceState({ i: index }, '', to)
  else history.pushState({ i: ++index }, '', to)
  return show(to, false, !replace)
}

window.addEventListener('popstate', (e) => {
  const i = (e.state as { i?: number } | null)?.i ?? 0
  const back = i < index
  index = i
  show(location.pathname, back, true)
})

/** A link inside the app: navigates without reloading, but keeps the browser's
 * own behaviour for new tabs (⌘/Ctrl/middle click) and downloads. */
export function Link({ to, onClick, ...props }: ComponentProps<'a'> & { to: string }) {
  return (
    <a
      href={to}
      {...props}
      onClick={(e) => {
        onClick?.(e)
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || props.target || props.download !== undefined) return
        e.preventDefault()
        navigate(to)
      }}
    />
  )
}
