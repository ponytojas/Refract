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
const leaving = new Set<() => Promise<void | boolean>>()
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

/** Runs fn (and waits for it) before the page changes. Resolving to false cancels the navigation. */
export function useBeforeLeave(fn: () => Promise<void | boolean>) {
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

// Saving edits must not be skipped, but a hung request must not trap the user.
// False when a page asked to stay.
async function leave(): Promise<boolean> {
  const results: (void | boolean)[] = await Promise.race([
    Promise.all([...leaving].map((f) => f().catch(() => undefined))),
    wait(5000).then(() => []),
  ])
  return !results.includes(false)
}

function show(to: string, back: boolean, animate: boolean) {
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
export async function navigate(to: string, { replace = false } = {}) {
  if (to === location.pathname && to === current) return
  if (!(await leave())) return
  if (replace) history.replaceState({ i: index }, '', to)
  else history.pushState({ i: ++index }, '', to)
  return show(to, false, !replace)
}

/**
 * Loads a page from the server instead of switching in place (no argument:
 * reloads this one). Needed when the session cookie has just changed (signing
 * in, out or deleting the account): the app's state, such as who is signed in,
 * was read under the old session. Like navigate it first waits for pages to
 * finish pending work (saving edits), but it doesn't let them cancel: the
 * session is already gone or changed.
 */
export async function reloadTo(to?: string) {
  await leave()
  if (to === undefined) location.reload()
  else location.assign(to)
}

let undoing = false
window.addEventListener('popstate', async (e) => {
  if (undoing) {
    undoing = false
    return
  }
  const i = (e.state as { i?: number } | null)?.i ?? 0
  const back = i < index
  if (!(await leave())) {
    // Stay: put the browser's history back where it was.
    if (index !== i) {
      undoing = true
      history.go(index - i)
    }
    return
  }
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
        if (
          e.defaultPrevented ||
          e.button !== 0 ||
          e.metaKey ||
          e.ctrlKey ||
          e.shiftKey ||
          e.altKey ||
          props.target ||
          props.download !== undefined
        )
          return
        e.preventDefault()
        navigate(to)
      }}
    />
  )
}
