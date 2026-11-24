// What was dropped on the project list: files, and folders read through the (non-standard but
// universal) FileSystemEntry API. Entries must be taken from the event synchronously.

export type DroppedFile = { file: File; path: string }
// tooBig: the walk passed the entry limit below (a huge tree, or a symlink loop with wide folders).
// tooDeep: it passed the depth limit (a loop, or an absurdly nested tree). error: an entry
// could not be read; the folder is skipped and the rest of the drop goes on.
export type DroppedFolder = { name: string; files: DroppedFile[]; tooBig?: boolean; tooDeep?: boolean; error?: string }
export type Dropped = { files: File[]; folders: DroppedFolder[] }

type Entry = {
  isFile: boolean
  isDirectory: boolean
  name: string
  file?: (ok: (f: File) => void, fail: (e: unknown) => void) => void
  createReader?: () => { readEntries: (ok: (e: Entry[]) => void, fail: (e: unknown) => void) => void }
}

type Item = { kind: string; getAsFile(): File | null; webkitGetAsEntry?: () => Entry | null }

// Far above what an import takes (the server counts files after skipping .git, so the
// real limit stays there), low enough that a loop or a dropped home folder ends.
export const maxEntries = 50_000
const maxDepth = 64

class TooBig extends Error {}
class TooDeep extends Error {}

// Children of a directory. readEntries returns batches: keep asking until it answers an empty
// one, but stop as soon as the entries seen so far pass the limit, before buffering the rest.
async function readAll(dir: Entry, seen: { n: number }): Promise<Entry[]> {
  const reader = dir.createReader!()
  const all: Entry[] = []
  for (;;) {
    const batch = await new Promise<Entry[]>((ok, fail) => reader.readEntries(ok, fail))
    if (batch.length === 0) return all
    all.push(...batch)
    if (seen.n + all.length > maxEntries) throw new TooBig()
  }
}

async function walk(entry: Entry, prefix: string, out: DroppedFile[], seen: { n: number }, depth: number) {
  if (depth > maxDepth) throw new TooDeep()
  if (++seen.n > maxEntries) throw new TooBig()
  const path = prefix + entry.name
  if (entry.isFile) {
    const file = await new Promise<File>((ok, fail) => entry.file!(ok, fail))
    out.push({ file, path })
  } else if (entry.isDirectory) {
    for (const child of await readAll(entry, seen)) await walk(child, path + '/', out, seen, depth + 1)
  }
}

export async function readDropped(items: Item[]): Promise<Dropped> {
  // Take every entry before the first await: the event's items are gone afterwards.
  const taken = items
    .filter((i) => i.kind === 'file')
    .map((i) => ({ entry: i.webkitGetAsEntry?.() ?? null, file: i.getAsFile() }))
  const result: Dropped = { files: [], folders: [] }
  for (const { entry, file } of taken) {
    if (entry?.isDirectory) {
      const files: DroppedFile[] = []
      try {
        await walk(entry, '', files, { n: 0 }, 0)
        result.folders.push({ name: entry.name, files })
      } catch (e) {
        if (e instanceof TooBig) result.folders.push({ name: entry.name, files: [], tooBig: true })
        else if (e instanceof TooDeep) result.folders.push({ name: entry.name, files: [], tooDeep: true })
        else result.folders.push({ name: entry.name, files: [], error: e instanceof Error ? e.message : String(e) })
      }
    } else if (file) {
      result.files.push(file)
    }
  }
  return result
}
