// Work that must reach the server before the app quits: saving edits. The
// desktop app calls window.refractFlush() (see "For packagers" in the README)
// before it stops the server, which can't see text still in the editor.

type Flusher = () => Promise<boolean>
const flushers = new Set<Flusher>()

/** Registers fn, which saves pending work and resolves to false when some couldn't be saved. Returns the unregister function. */
export function onFlush(fn: Flusher): () => void {
  flushers.add(fn)
  return () => void flushers.delete(fn)
}

/**
 * Runs every registered flush and resolves to true when everything is saved,
 * false when something couldn't be (a failed or locked save, or none finished
 * within timeoutMs): the caller should ask before quitting.
 */
export async function flushPending(timeoutMs = 5000): Promise<boolean> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timedOut = new Promise<boolean[]>((r) => (timer = setTimeout(() => r([false]), timeoutMs)))
  const all = Promise.all([...flushers].map((f) => f().catch(() => false)))
  try {
    return (await Promise.race([all, timedOut])).every(Boolean)
  } finally {
    clearTimeout(timer)
  }
}
