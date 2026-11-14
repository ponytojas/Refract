// Debounced, per-file autosave. Independent of which tab is visible: a file's
// pending content is saved even after its tab is switched away or closed.
// Saves of the same file are serialized, so an older save can never land
// after (and overwrite) a newer one.

export type SaveStatus = 'saved' | 'dirty' | 'saving' | 'error'
type SaveFn = (path: string, content: string) => Promise<void>

type FileState = {
  pending?: string // latest content not yet handed to save()
  timer?: ReturnType<typeof setTimeout>
  inflight?: Promise<void>
  status: SaveStatus
}

export class Autosaver {
  private files = new Map<string, FileState>()
  private paused = false
  private save: SaveFn
  private delay: number
  private retryDelay: number
  private onStatus: (path: string, status: SaveStatus) => void

  constructor(
    save: SaveFn,
    opts: { delay?: number; retryDelay?: number; onStatus?: (path: string, status: SaveStatus) => void } = {},
  ) {
    this.save = save
    this.delay = opts.delay ?? 800
    this.retryDelay = opts.retryDelay ?? 3000
    this.onStatus = opts.onStatus ?? (() => {})
  }

  /** Records new content for path and (re)starts its debounce timer. */
  change(path: string, content: string) {
    const f = this.file(path)
    f.pending = content
    this.schedule(path, f, this.delay)
    this.set(path, f, 'dirty')
  }

  /** Saves path now if it has unsaved content; resolves when everything for it is on disk. */
  async flush(path: string): Promise<void> {
    const f = this.files.get(path)
    if (!f) return
    clearTimeout(f.timer)
    f.timer = undefined
    while (f.inflight) await f.inflight
    if (f.pending === undefined || this.paused) return
    const content = f.pending
    f.pending = undefined
    this.set(path, f, 'saving')
    f.inflight = this.save(path, content)
      .then(() => {
        if (f.pending === undefined) this.set(path, f, 'saved')
      })
      .catch(() => {
        // Keep the content unless something newer arrived meanwhile, and retry.
        if (f.pending === undefined) f.pending = content
        // Paused (the project can't be written): not an error to retry, just unsaved.
        this.set(path, f, this.paused ? 'dirty' : 'error')
        this.schedule(path, f, this.retryDelay)
      })
      .finally(() => {
        f.inflight = undefined
      })
    await f.inflight
  }

  /**
   * Stops saving without losing anything: pending content stays in memory, no
   * timer fires, and a failing save isn't retried. isDirty() stays true, so
   * leaving the page still warns.
   */
  pause() {
    this.paused = true
    for (const [path, f] of this.files) {
      clearTimeout(f.timer)
      f.timer = undefined
      if (f.pending !== undefined && f.status !== 'dirty') this.set(path, f, 'dirty')
    }
  }

  /** Starts saving again and saves everything that is pending. */
  resume(): Promise<void> {
    this.paused = false
    return this.flushAll()
  }

  get isPaused(): boolean {
    return this.paused
  }

  /** The unsaved content, per file. */
  unsaved(): [path: string, content: string][] {
    return [...this.files].flatMap(([path, f]) =>
      f.pending === undefined ? [] : [[path, f.pending] as [string, string]],
    )
  }

  flushAll(): Promise<void> {
    return Promise.all([...this.files.keys()].map((p) => this.flush(p))).then(() => {})
  }

  isDirty(path?: string): boolean {
    const dirty = (f: FileState) => f.pending !== undefined || f.inflight !== undefined
    return path === undefined
      ? [...this.files.values()].some(dirty)
      : !!this.files.get(path) && dirty(this.files.get(path)!)
  }

  status(path: string): SaveStatus {
    return this.files.get(path)?.status ?? 'saved'
  }

  /** Follows a rename: pending content is saved under the new path. */
  rename(from: string, to: string) {
    const f = this.files.get(from)
    if (!f) return
    this.files.delete(from)
    this.files.set(to, f)
    if (f.timer) this.schedule(to, f, this.delay)
  }

  /** Forgets a deleted file without saving it. */
  drop(path: string) {
    const f = this.files.get(path)
    if (f) clearTimeout(f.timer)
    this.files.delete(path)
  }

  private file(path: string): FileState {
    let f = this.files.get(path)
    if (!f) this.files.set(path, (f = { status: 'saved' }))
    return f
  }

  private schedule(path: string, f: FileState, ms: number) {
    if (this.paused) return
    clearTimeout(f.timer)
    f.timer = setTimeout(() => {
      f.timer = undefined
      this.flush(path)
    }, ms)
  }

  private set(path: string, f: FileState, status: SaveStatus) {
    f.status = status
    this.onStatus(path, status)
  }
}
