// What the compile chip and its notice show, from the compile's result and
// the activity the server reports while it runs. No React, so it is testable.

export type Downloads = {
  fetched: number // files downloaded and kept this run
  missing: string[] // files that could not be downloaded
  offline: boolean // stopped because the package server can't be reached
  interrupted?: boolean // time ran out while downloading
}

export type Activity = {
  state: 'idle' | 'compiling' | 'downloading'
  file: string
  downloaded: number
  retrying: boolean
}

export type CompileView =
  | { kind: 'idle' }
  | { kind: 'compiling' }
  | { kind: 'downloading'; file: string; count: number }
  | { kind: 'retrying' }
  | { kind: 'needs-download'; files: string[] }
  | { kind: 'interrupted' }
  | { kind: 'downloaded'; count: number }

type Input = {
  compiling: boolean
  activity: Activity | null
  result: { downloads?: Downloads; superseded?: boolean } | null
}

export function compileView({ compiling, activity, result }: Input): CompileView {
  if (compiling) {
    if (activity?.retrying) return { kind: 'retrying' }
    if (activity?.state === 'downloading')
      return { kind: 'downloading', file: activity.file, count: activity.downloaded }
    return { kind: 'compiling' }
  }
  const d = result?.downloads
  if (!result || result.superseded || !d) return { kind: 'idle' }
  if (d.missing.length > 0 || d.offline) return { kind: 'needs-download', files: d.missing }
  if (d.interrupted) return { kind: 'interrupted' }
  if (d.fetched > 0) return { kind: 'downloaded', count: d.fetched }
  return { kind: 'idle' }
}

/** Whether the PDF viewer should reload: a compile that stopped for a missing download leaves the previous PDF. */
export function pdfChanged(result: { pdf?: string; downloads?: Downloads }): boolean {
  return !!result.pdf && !(result.downloads?.missing.length ?? 0)
}
