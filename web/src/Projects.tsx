import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import {
  ArrowDownUp,
  ArrowLeft,
  ChevronDown,
  Download,
  FileArchive,
  FolderOpen,
  FolderUp,
  MoreHorizontal,
  Pencil,
  Plus,
  RotateCcw,
  Search,
  Trash2,
  Upload,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  api,
  download,
  errorText,
  type ChatGPTStatus,
  type ImportResult,
  type Project,
  type Trash,
  type TrashedProject,
} from './api'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import AccountMenu from '@/components/AccountMenu'
import PaperIcon from '@/components/PaperIcon'
import { ago } from '@/lib/time'
import { daysLeft } from '@/lib/trash'
import { cn } from '@/lib/utils'
import { t, tn } from '@/lib/i18n'
import { useCommands } from '@/lib/commands'
import { DropdownItems, WithContextMenu, type MenuEntry } from '@/components/Menu'
import { Link, navigate, pageReady, previousPath, usePath } from '@/lib/router'

type Sort = { key: 'name' | 'updated'; desc: boolean }

export default function Projects(props: { status: ChatGPTStatus | null }) {
  return usePath() === '/trash' ? <TrashView {...props} /> : <ProjectList {...props} />
}

function ProjectList({ status }: { status: ChatGPTStatus | null }) {
  const [projects, setProjects] = useState<Project[] | null>(null)
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>({ key: 'updated', desc: true })
  const [dragging, setDragging] = useState(false)
  const [busy, setBusy] = useState(false)
  const [naming, setNaming] = useState<{ mode: 'new' } | { mode: 'rename'; project: Project } | null>(null)
  const [name, setName] = useState('')
  const [reports, setReports] = useState<ImportResult[]>([])
  const zipInput = useRef<HTMLInputElement>(null)
  const folderInput = useRef<HTMLInputElement>(null)

  const rowMenu = (p: Project): MenuEntry[] => [
    { label: t('Open'), icon: FolderOpen, onSelect: () => navigate(`/p/${p.id}`) },
    {
      label: t('Rename'),
      icon: Pencil,
      onSelect: () => {
        setName(p.name)
        setNaming({ mode: 'rename', project: p })
      },
    },
    {
      label: t('Export .zip'),
      icon: Download,
      onSelect: () => download(`/api/projects/${p.id}/export`, `${p.name}.zip`).catch((e) => toast.error(errorText(e))),
    },
    '-',
    { label: t('Move to trash'), icon: Trash2, onSelect: () => moveToTrash(p) },
  ]

  useCommands(() => [
    {
      id: 'p:new',
      group: 'Actions',
      label: t('New blank project'),
      icon: Plus,
      run: () => {
        setName('')
        setNaming({ mode: 'new' })
      },
    },
    {
      id: 'p:zip',
      group: 'Actions',
      label: t('Import a Prism export (.zip)'),
      icon: FileArchive,
      keywords: 'upload',
      run: () => zipInput.current?.click(),
    },
    {
      id: 'p:folder',
      group: 'Actions',
      label: t('Import a folder'),
      icon: FolderUp,
      keywords: 'upload',
      run: () => folderInput.current?.click(),
    },
    { id: 'p:trash', group: 'Navigate', label: t('Trash'), icon: Trash2, run: () => navigate('/trash') },
    ...(projects ?? []).map((p) => ({
      id: `project:${p.id}`,
      group: 'Projects',
      label: p.name,
      hint: p.main_file || undefined,
      icon: FolderOpen,
      run: () => navigate(`/p/${p.id}`),
    })),
  ])

  const load = () =>
    api<Project[]>('/api/projects')
      .then(setProjects)
      .catch((e) => toast.error(errorText(e)))
  useEffect(() => {
    document.title = 'Refract'
    load()
  }, [])
  useEffect(() => {
    if (projects) pageReady()
  }, [projects])

  // Coming back from a project, its title glides from the workspace header into
  // its row; opening one, the clicked row's title glides into the header.
  // (view-transition-name must be unique on the page, hence one row at a time.)
  const [cameFrom, setCameFrom] = useState(() => previousPath().match(/^\/p\/([^/]+)/)?.[1] ?? '')
  useEffect(() => {
    const timer = setTimeout(() => setCameFrom(''), 600)
    return () => clearTimeout(timer)
  }, [])
  const morphInto = (e: React.MouseEvent<HTMLAnchorElement>) => {
    document.querySelectorAll<HTMLElement>('[data-vt-title]').forEach((el) => (el.style.viewTransitionName = ''))
    const title = e.currentTarget.querySelector<HTMLElement>('[data-vt-title]')
    if (title) title.style.viewTransitionName = 'project-title'
  }

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    const list = (projects ?? []).filter((p) => !q || p.name.toLowerCase().includes(q))
    const dir = sort.desc ? -1 : 1
    return list.sort((a, b) =>
      sort.key === 'name'
        ? dir * a.name.localeCompare(b.name)
        : dir * (new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime()),
    )
  }, [projects, query, sort])

  // Import results worth a look: warnings, skipped files, or an unknown main file.
  const attention = (r: ImportResult) => r.warnings.length > 0 || !!r.skipped?.length || !!r.candidates

  async function importZips(files: File[]) {
    const zips = files.filter((f) => f.name.toLowerCase().endsWith('.zip'))
    if (zips.length === 0) return toast.error(t('Drop .zip files to import them.'))
    setBusy(true)
    const done: ImportResult[] = []
    for (const f of zips) {
      const projectName = f.name.replace(/\.zip$/i, '')
      try {
        done.push(
          await api<ImportResult>(`/api/projects/import?name=${encodeURIComponent(projectName)}`, {
            method: 'POST',
            body: f,
          }),
        )
      } catch (e) {
        toast.error(`${projectName}: ${errorText(e)}`)
      }
    }
    finishImport(done)
  }

  async function importFolder(files: File[]) {
    if (files.length === 0) return
    const folder = files[0].webkitRelativePath.split('/')[0]
    const form = new FormData()
    for (const f of files) form.append('file', f, f.webkitRelativePath)
    setBusy(true)
    try {
      finishImport([
        await api<ImportResult>(`/api/projects/import-folder?name=${encodeURIComponent(folder)}`, {
          method: 'POST',
          body: form,
        }),
      ])
    } catch (e) {
      toast.error(`${folder}: ${errorText(e)}`)
      setBusy(false)
    }
  }

  function finishImport(done: ImportResult[]) {
    setBusy(false)
    load()
    if (done.length === 0) return
    toast.success(
      done.length === 1
        ? t('Imported {name}', { name: done[0].project.name })
        : t('Imported {n} projects', { n: done.length }),
    )
    const needLook = done.filter(attention)
    if (needLook.length) setReports(needLook)
  }

  async function submitName() {
    const n = name.trim()
    if (!n || !naming) return
    try {
      if (naming.mode === 'new') {
        const p = await api<Project>('/api/projects', { method: 'POST', body: { name: n } })
        setNaming(null)
        navigate(`/p/${p.id}`)
        return
      }
      await api(`/api/projects/${naming.project.id}`, { method: 'PATCH', body: { name: n } })
      setNaming(null)
      load()
    } catch (e) {
      toast.error(errorText(e))
    }
  }

  // The row leaves at once; Undo (in the toast) brings the project back.
  async function moveToTrash(p: Project) {
    setProjects((list) => list && list.filter((x) => x.id !== p.id))
    try {
      await api(`/api/projects/${p.id}`, { method: 'DELETE' })
    } catch (e) {
      load()
      toast.error(errorText(e))
      return
    }
    toast(t('Moved {name} to the trash', { name: p.name }), {
      duration: 10000,
      action: { label: t('Undo'), onClick: () => restore(p) },
    })
  }

  async function restore(p: Project) {
    try {
      await api(`/api/projects/${p.id}/restore`, { method: 'POST' })
    } catch (e) {
      toast.error(errorText(e))
    }
    load()
  }

  function onDrop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    importZips([...e.dataTransfer.files])
  }

  const sortBy = (key: Sort['key']) => setSort((s) => ({ key, desc: s.key === key ? !s.desc : key === 'updated' }))

  return (
    <div
      className="flex h-full flex-col p-2"
      onDragOver={(e) => {
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
      onDrop={onDrop}
    >
      <main className="relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card shadow-xs">
        <header className="flex flex-wrap items-center gap-2 px-5 py-4">
          <h1 className="mr-auto flex items-center gap-2.5 text-lg font-semibold tracking-tight">
            <img src="/logo.svg" alt="Refract" className="size-7" style={{ viewTransitionName: 'logo' }} />
            {t('All projects')}
          </h1>
          <div className="relative w-full max-w-72 sm:w-72">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('Search')}
              aria-label={t('Search projects')}
              className="rounded-full pl-9"
            />
          </div>
          <Link to="/trash" className={cn(buttonVariants({ variant: 'ghost' }), 'rounded-full')}>
            <Trash2 /> {t('Trash')}
          </Link>
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="secondary" className="rounded-full" disabled={busy} />}>
              {busy ? t('Importing…') : t('Import')} <ChevronDown className="text-muted-foreground" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuItem onClick={() => zipInput.current?.click()}>
                <FileArchive /> {t('Zip files…')}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => folderInput.current?.click()}>
                <FolderUp /> {t('Folder…')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button className="rounded-full" />}>
              <Plus /> {t('New')} <ChevronDown className="opacity-60" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuItem
                onClick={() => {
                  setName('')
                  setNaming({ mode: 'new' })
                }}
              >
                <Plus /> {t('Blank project')}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => zipInput.current?.click()}>
                <Upload /> {t('From a Prism export…')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <input
            ref={zipInput}
            type="file"
            accept=".zip"
            multiple
            hidden
            onChange={(e) => importZips([...(e.target.files ?? [])])}
          />
          <input
            ref={folderInput}
            type="file"
            // @ts-expect-error webkitdirectory is non-standard but supported by all desktop browsers
            webkitdirectory=""
            hidden
            onChange={(e) => importFolder([...(e.target.files ?? [])])}
          />
        </header>

        <div className="flex items-center border-y px-5 py-2 text-xs text-muted-foreground">
          <button
            className="flex items-center gap-1 transition-colors duration-150 hover:text-foreground"
            onClick={() => sortBy('name')}
          >
            {t('Name')} <ArrowDownUp className={cn('size-3', sort.key !== 'name' && 'opacity-40')} />
          </button>
          <button
            className="ml-auto flex items-center gap-1 pr-11 transition-colors duration-150 hover:text-foreground"
            onClick={() => sortBy('updated')}
          >
            {t('Updated')} <ArrowDownUp className={cn('size-3', sort.key !== 'updated' && 'opacity-40')} />
          </button>
        </div>

        <ul className="min-h-0 flex-1 overflow-auto">
          {shown.map((p, i) => (
            <li
              key={p.id}
              className="group enter border-b last:border-b-0"
              style={{ transitionDelay: `${Math.min(i, 8) * 30}ms` }}
            >
              <WithContextMenu items={() => rowMenu(p)}>
                <div className="flex items-center gap-4 px-5 py-2.5 transition-colors duration-150 hover:bg-accent/50">
                  <Link
                    to={`/p/${p.id}`}
                    onClick={morphInto}
                    className="flex min-w-0 flex-1 items-center gap-4 outline-none focus-visible:underline"
                  >
                    <PaperIcon />
                    <span className="min-w-0">
                      <span
                        data-vt-title
                        className="block w-fit max-w-full truncate text-sm font-medium"
                        style={p.id === cameFrom ? { viewTransitionName: 'project-title' } : undefined}
                      >
                        {p.name}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        {p.main_file || t('Main file not set')}
                      </span>
                    </span>
                  </Link>
                  <span
                    className="w-12 text-right text-xs text-muted-foreground tabular-nums"
                    title={new Date(p.updated_at).toLocaleString()}
                  >
                    {ago(p.updated_at)}
                  </span>
                  <DropdownMenu>
                    <DropdownMenuTrigger
                      render={
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label={t('Actions for {name}', { name: p.name })}
                          className="text-muted-foreground"
                        />
                      }
                    >
                      <MoreHorizontal />
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-44">
                      <DropdownItems items={rowMenu(p)} />
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </WithContextMenu>
            </li>
          ))}
        </ul>

        {projects && shown.length === 0 && (
          <div className="absolute inset-0 top-28 flex enter flex-col items-center justify-center gap-3 text-center">
            <PaperIcon className="h-12 w-9 opacity-70" />
            <p className="text-sm font-medium">{query ? t('No projects match.') : t('No projects yet')}</p>
            {!query && (
              <p className="max-w-xs text-sm text-muted-foreground">
                {t('Create a blank project, or drop Prism .zip exports anywhere on this page.')}
              </p>
            )}
          </div>
        )}

        {dragging && (
          <div className="pointer-events-none absolute inset-2 flex items-center justify-center rounded-lg border-2 border-dashed border-ring bg-background/80 text-sm font-medium backdrop-blur-sm">
            {t('Drop .zip files to import')}
          </div>
        )}
      </main>
      <footer className="flex items-center px-1 pt-2">
        <AccountMenu status={status} />
      </footer>

      <Dialog open={!!naming} onOpenChange={(o) => !o && setNaming(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{naming?.mode === 'new' ? t('New project') : t('Rename project')}</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              submitName()
            }}
          >
            <Input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('Project name')}
              aria-label={t('Project name')}
            />
            <DialogFooter className="mt-4">
              <Button type="button" variant="ghost" onClick={() => setNaming(null)}>
                {t('Cancel')}
              </Button>
              <Button type="submit" disabled={!name.trim()}>
                {naming?.mode === 'new' ? t('Create') : t('Save')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={reports.length > 0} onOpenChange={(o) => !o && setReports([])}>
        <DialogContent className="max-h-[80vh] overflow-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('Imported, with notes')}</DialogTitle>
            <DialogDescription>
              {t("These won't stop you from working, but they may affect compiling on another machine.")}
            </DialogDescription>
          </DialogHeader>
          {reports.map((r) => (
            <ImportNotes key={r.project.id} result={r} onMainFile={load} />
          ))}
        </DialogContent>
      </Dialog>
    </div>
  )
}

function TrashView({ status }: { status: ChatGPTStatus | null }) {
  const [trash, setTrash] = useState<Trash | null>(null)
  const [forever, setForever] = useState<TrashedProject | null>(null)

  const load = () =>
    api<Trash>('/api/trash')
      .then(setTrash)
      .catch((e) => toast.error(errorText(e)))
  useEffect(() => {
    document.title = `${t('Trash')} · Refract`
    load()
  }, [])
  useEffect(() => {
    if (trash) pageReady()
  }, [trash])

  const without = (id: string) =>
    setTrash((cur) => cur && { ...cur, projects: cur.projects.filter((x) => x.id !== id) })

  async function restore(p: TrashedProject) {
    try {
      await api(`/api/projects/${p.id}/restore`, { method: 'POST' })
    } catch (e) {
      toast.error(errorText(e))
      load()
      return
    }
    without(p.id)
    toast.success(t('Restored {name}', { name: p.name }), {
      action: { label: t('Open'), onClick: () => navigate(`/p/${p.id}`) },
    })
  }

  async function deleteForever() {
    const p = forever
    if (!p) return
    setForever(null)
    try {
      await api(`/api/projects/${p.id}/purge`, { method: 'DELETE' })
    } catch (e) {
      toast.error(errorText(e))
      load()
      return
    }
    without(p.id)
    toast.success(t('Deleted {name} for good', { name: p.name }))
  }

  const days = trash?.days ?? 30
  const list = trash?.projects ?? []
  return (
    <div className="flex h-full flex-col p-2">
      <main className="relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card shadow-xs">
        <header className="flex flex-col gap-1 px-5 py-4">
          <Link
            to="/"
            className="mb-1 flex w-fit items-center gap-1.5 text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" /> {t('All projects')}
          </Link>
          <h1 className="flex items-center gap-2.5 text-lg font-semibold tracking-tight">
            <Trash2 className="size-5 text-muted-foreground" />
            {t('Trash')}
          </h1>
          <p className="text-sm text-muted-foreground">
            {tn(
              days,
              'Deleted projects stay here for {n} day, then they are deleted for good.',
              'Deleted projects stay here for {n} days, then they are deleted for good.',
            )}
          </p>
        </header>
        <ul className="min-h-0 flex-1 overflow-auto border-t">
          {list.map((p, i) => {
            const left = daysLeft(p.purge_at)
            return (
              <li
                key={p.id}
                className="flex enter items-center gap-4 border-b px-5 py-2.5 last:border-b-0"
                style={{ transitionDelay: `${Math.min(i, 8) * 30}ms` }}
              >
                <PaperIcon />
                <span
                  className="min-w-0 flex-1"
                  title={t('Deleted on {date}', { date: new Date(p.deleted_at).toLocaleString() })}
                >
                  <span className="block truncate text-sm font-medium">{p.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {tn(left, 'Deleted for good in {n} day', 'Deleted for good in {n} days')}
                  </span>
                </span>
                <Button variant="secondary" size="sm" aria-label={t('Restore')} onClick={() => restore(p)}>
                  <RotateCcw /> <span className="hidden sm:inline">{t('Restore')}</span>
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t('Delete forever')}
                  className="text-destructive hover:text-destructive"
                  onClick={() => setForever(p)}
                >
                  <Trash2 /> <span className="hidden sm:inline">{t('Delete forever')}</span>
                </Button>
              </li>
            )
          })}
        </ul>
        {trash && list.length === 0 && (
          <div className="absolute inset-0 top-28 flex enter flex-col items-center justify-center gap-3 text-center">
            <Trash2 className="size-10 opacity-40" />
            <p className="text-sm font-medium">{t('The trash is empty')}</p>
          </div>
        )}
      </main>
      <footer className="flex items-center px-1 pt-2">
        <AccountMenu status={status} />
      </footer>

      <AlertDialog open={!!forever} onOpenChange={(o) => !o && setForever(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete {name} forever?', { name: forever?.name ?? '' })}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("Its files, history and chats are removed from this server. This can't be undone.")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={deleteForever}>
              {t('Delete forever')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ImportNotes({ result: r, onMainFile }: { result: ImportResult; onMainFile: () => void }) {
  const [main, setMain] = useState(r.project.main_file)
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-semibold">{r.project.name}</h3>
      {r.candidates && (
        <div className="flex items-center gap-2 text-sm">
          <span className="text-muted-foreground">{t('Main file not found. Choose it:')}</span>
          <Select
            value={main || null}
            onValueChange={(v) =>
              v &&
              api(`/api/projects/${r.project.id}`, { method: 'PATCH', body: { main_file: v } })
                .then(() => {
                  setMain(v)
                  onMainFile()
                })
                .catch((e) => toast.error(errorText(e)))
            }
          >
            <SelectTrigger size="sm" className="w-56">
              <SelectValue placeholder={t('Select…')} />
            </SelectTrigger>
            <SelectContent>
              {r.candidates.map((c) => (
                <SelectItem key={c} value={c}>
                  {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      {r.skipped && r.skipped.length > 0 && (
        <p className="text-xs text-muted-foreground">{t('Skipped: {files}', { files: r.skipped.join(', ') })}</p>
      )}
      <ul className="space-y-2">
        {r.warnings.map((w, i) => (
          <li key={i} className="rounded-md bg-muted/60 px-3 py-2 text-sm">
            <code className="text-xs text-muted-foreground">
              {w.file}:{w.line}
            </code>
            <p className="mt-0.5">{w.message}</p>
          </li>
        ))}
      </ul>
    </section>
  )
}
