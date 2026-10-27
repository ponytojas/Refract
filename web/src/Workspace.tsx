import { useCallback, useEffect, useRef, useState } from 'react'
import type { EditorState } from '@codemirror/state'
import { usePanelRef } from 'react-resizable-panels'
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  BookMarked,
  Check,
  Copy,
  Star,
  ChevronDown,
  ChevronLeft,
  File,
  FileText,
  FolderOpen,
  FolderPlus,
  GitBranch,
  History as HistoryIcon,
  Loader2,
  MessageSquare,
  MessageSquarePlus,
  PanelLeft,
  Play,
  Plus,
  Sparkles,
  Upload,
  X,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  api,
  download,
  errorText,
  fileURL,
  mkdir,
  move,
  readFile,
  removeFile,
  writeFile,
  type ChatGPTStatus,
  type FileEntry,
  type OpenedFile,
  type Project,
} from './api'
import Assistant, { type Chat, type EditorContext, type Turn } from './Assistant'
import Review from './Review'
import History from './History'
import GitDialog, { pulledMessage, type PullResult } from './GitDialog'
import CiteDialog, { type Citation, type CiteTarget } from './CiteDialog'
import { Autosaver, type SaveStatus } from './autosave'
import { Link, navigate, pageReady, useBeforeLeave } from '@/lib/router'
import CompileStatus, { type CompileResult, type Problem } from './CompileStatus'
import Editor from './Editor'
import FileTree from './FileTree'
import PdfViewer, { type Highlight } from './PdfViewer'
import AccountMenu from '@/components/AccountMenu'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
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
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { ago } from '@/lib/time'
import { WithContextMenu, type MenuEntry } from '@/components/Menu'
import { useCommands } from '@/lib/commands'
import { usePrefs } from '@/lib/prefs'
import { editorShortcuts, mod } from '@/lib/shortcuts'
import { cn } from '@/lib/utils'
import { t } from '@/lib/i18n'

const under = (p: string, prefix: string) => p === prefix || p.startsWith(prefix + '/')
const moved = (p: string, from: string, to: string) => (under(p, from) ? to + p.slice(from.length) : p)

const saveState: Record<SaveStatus, { text: string; dot: string }> = {
  saved: { text: t('Saved'), dot: 'bg-success' },
  dirty: { text: t('Unsaved changes'), dot: 'bg-warning' },
  saving: { text: t('Saving…'), dot: 'bg-muted-foreground animate-pulse' },
  error: { text: t('Save failed, retrying…'), dot: 'bg-destructive' },
}

type Ask = { title: string; description?: string; value: string; action: string; onSubmit: (v: string) => Promise<void> }
type Confirm = { title: string; description: string; action: string; onConfirm: () => Promise<void> }

export default function Workspace({ id, status }: { id: string; status: ChatGPTStatus | null }) {
  const [project, setProject] = useState<Project | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [files, setFiles] = useState<FileEntry[]>([])
  const [tabs, setTabs] = useState<string[]>([])
  const [active, setActive] = useState<string | undefined>()
  const [statuses, setStatuses] = useState<Record<string, SaveStatus>>({})
  const [compiling, setCompiling] = useState(false)
  const [result, setResult] = useState<CompileResult | null>(null)
  const [compileError, setCompileError] = useState('')
  const [jump, setJump] = useState<{ path: string; line: number; n: number } | undefined>()
  const [pdfVersion, setPdfVersion] = useState(0)
  const [highlight, setHighlight] = useState<Highlight | undefined>()
  const [chatOpen, setChatOpen] = useState(false)
  const [chatFocus, setChatFocus] = useState(0)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [gitOpen, setGitOpen] = useState(false)
  const prefs = usePrefs()
  const [citing, setCiting] = useState<{ target: CiteTarget | null; suggest: boolean } | null>(null)
  const [proposal, setProposal] = useState<{ path: string; from: number; to: number; original: string; text: string } | undefined>()
  const [insert, setInsert] = useState<{ path: string; text: string } | undefined>()
  const [aiRequest, setAiRequest] = useState<{ text: string; n: number } | undefined>()
  const [selLines, setSelLines] = useState('')
  const [sideTab, setSideTab] = useState<'files' | 'chats'>('files')
  const [review, setReview] = useState<Turn | null>(null)
  const [turnUpdate, setTurnUpdate] = useState<Turn | undefined>()
  const [editorKey, setEditorKey] = useState(0) // bump to reload open files from disk
  const [chats, setChats] = useState<Chat[]>([])
  const [chatId, setChatId] = useState('')
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [ask, setAsk] = useState<Ask | null>(null)
  const [askValue, setAskValue] = useState('')
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const sidebar = usePanelRef()
  const uploadInput = useRef<HTMLInputElement>(null)

  // Outside React state: content loaded from the server and live editor states per open file.
  const loaded = useRef(new Map<string, OpenedFile>())
  const states = useRef(new Map<string, EditorState>())
  const saver = useRef<Autosaver | null>(null)
  if (!saver.current) {
    saver.current = new Autosaver((p, c) => writeFile(id, p, c), {
      onStatus: (p, s) => setStatuses((prev) => ({ ...prev, [p]: s })),
    })
  }
  const autosave = saver.current
  // Leaving for another page: everything typed is on the server first.
  useBeforeLeave(useCallback(() => autosave.flushAll(), [autosave]))

  const loadFiles = useCallback(() => api<FileEntry[]>(`/api/projects/${id}/files`).then(setFiles), [id])
  const loadChats = useCallback(
    () =>
      api<Chat[]>(`/api/projects/${id}/chats`)
        .then((cs) => {
          setChats(cs)
          setChatId((cur) => cur || cs[0]?.id || '')
        })
        .catch(() => {}),
    [id],
  )

  // The navigation's transition waits for these, so it ends on the project.
  const [shown, setShown] = useState(false)
  useEffect(() => {
    if (shown) pageReady()
  }, [shown])

  useEffect(() => {
    const p1 = api<Project>(`/api/projects/${id}`)
      .then((p) => {
        setProject(p)
        document.title = `${p.name} · Refract`
      })
      .catch((e) => toast.error(errorText(e)))
    api<Project[]>('/api/projects')
      .then(setProjects)
      .catch(() => {})
    const p2 = loadFiles().catch((e) => toast.error(errorText(e)))
    Promise.all([p1, p2]).then(() => setShown(true))
    loadChats()
  }, [id, loadFiles, loadChats])

  // Never lose edits on reload or close: flush and ask the browser to confirm.
  useEffect(() => {
    const onUnload = (e: BeforeUnloadEvent) => {
      if (!autosave.isDirty()) return
      autosave.flushAll()
      e.preventDefault()
    }
    window.addEventListener('beforeunload', onUnload)
    return () => window.removeEventListener('beforeunload', onUnload)
  }, [autosave])

  async function compile() {
    setCompiling(true)
    setCompileError('')
    try {
      await autosave.flushAll() // compile what is on screen
      const res = await api<CompileResult>(`/api/projects/${id}/compile`, { method: 'POST' })
      setResult(res)
      if (res.pdf) setPdfVersion((v) => v + 1)
    } catch (e) {
      setCompileError(errorText(e))
    } finally {
      setCompiling(false)
    }
  }
  const compileRef = useRef(compile)
  compileRef.current = compile
  const closeRef = useRef(() => {})
  closeRef.current = () => {
    if (review) setReview(null)
    else if (active) close(active)
  }

  // Cmd/Ctrl+Enter anywhere in the workspace (the editor handles it itself when focused).
  // Cmd/Ctrl+W closes the open file. Browsers keep that shortcut for themselves
  // in a normal tab; it reaches the page when Refract is installed as an app.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && !e.defaultPrevented) {
        e.preventDefault()
        compileRef.current()
      }
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'w') {
        e.preventDefault()
        closeRef.current()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Forward sync: editor position → PDF.
  async function syncToPDF(path: string, line: number, col: number) {
    try {
      const { boxes } = await api<{ boxes: Omit<Highlight, 'n'>[] }>(
        `/api/projects/${id}/synctex/forward?file=${encodeURIComponent(path)}&line=${line}&col=${col}`,
      )
      setHighlight((h) => ({ ...boxes[0], n: (h?.n ?? 0) + 1 }))
    } catch (e) {
      toast(errorText(e))
    }
  }

  // Inverse sync: PDF point → editor.
  async function syncToSource(page: number, x: number, y: number) {
    try {
      const r = await api<{ file: string; line: number }>(`/api/projects/${id}/synctex/inverse?page=${page}&x=${x}&y=${y}`)
      await jumpTo(r.file, r.line)
    } catch (e) {
      toast(errorText(e))
    }
  }

  // What the assistant sees of the editor: the open file and the selection.
  function editorContext(): EditorContext {
    const st = active ? states.current.get(active) : undefined
    if (!active || !st) return { open_file: active ?? '', selection: '', from_line: 0, to_line: 0 }
    const sel = st.selection.main
    return {
      open_file: active,
      selection: st.sliceDoc(sel.from, sel.to),
      from_line: st.doc.lineAt(sel.from).number,
      to_line: st.doc.lineAt(sel.to).number,
    }
  }

  // Accepted AI changes rewrote files on disk: reload any that are open.
  async function reloadFromDisk(paths: string[]) {
    for (const p of paths) {
      if (!loaded.current.has(p)) continue
      states.current.delete(p)
      try {
        loaded.current.set(p, await readFile(id, p))
      } catch {
        forget([p]) // deleted by the change
      }
    }
    setEditorKey((k) => k + 1)
    await loadFiles()
  }

  async function acceptTurn(files: string[] | null): Promise<string[] | null> {
    if (!review) return null
    await autosave.flushAll()
    const res = await api<{ turn: Turn; conflicts: string[] | null }>(`/api/turns/${review.id}/accept`, { method: 'POST', body: { files } })
    if (res.conflicts?.length) return res.conflicts
    const applied = files ?? review.files.map((f) => f.path)
    await reloadFromDisk(applied)
    setTurnUpdate(res.turn)
    setReview(res.turn.status === 'review' ? res.turn : null)
    toast.success(applied.length === 1 ? t('Applied changes to {path}', { path: applied[0] }) : t('Applied changes to {n} files', { n: applied.length }))
    return null
  }

  async function rejectTurn() {
    if (!review) return
    const res = await api<{ turn: Turn }>(`/api/turns/${review.id}/reject`, { method: 'POST' })
    setTurnUpdate(res.turn)
    setReview(null)
  }

  async function pulled(res: PullResult) {
    if (res.changed.length) {
      await reloadFromDisk(tabs)
      setReview(null)
    }
    const msg = pulledMessage(res)
    if (res.conflicts.length) toast.error(msg)
    else toast.success(msg)
  }

  // Push/pull from the palette, without opening the dialog.
  async function sync(op: 'push' | 'pull') {
    await autosave.flushAll()
    try {
      if (op === 'push') {
        await api(`/api/projects/${id}/remote/push`, { method: 'POST' })
        toast.success(t('Pushed to the remote'))
      } else await pulled(await api<PullResult>(`/api/projects/${id}/remote/pull`, { method: 'POST' }))
    } catch (e) {
      toast.error(errorText(e))
    }
  }

  async function cited(c: Citation, sel: CiteTarget | null) {
    await reloadFromDisk([c.path]) // the .bib may be open
    const setup = c.needs_setup
      ? t('No .tex file loads a bibliography yet: add \\bibliography{{base}} (or \\addbibresource{{path}}).', {
          base: c.path.replace(/\.bib$/, ''),
          path: c.path,
        })
      : undefined
    if (sel && c.text) {
      // Placed by the assistant in the selection: shown as a diff to accept (Tab) or reject (Esc).
      setProposal({ path: sel.path, from: sel.from, to: sel.to, original: sel.text, text: c.text })
      toast.success(c.placed ? t('Citation placed: review it in the editor.') : t('Citation added at the end of the selection: review it in the editor.'), {
        description: setup,
      })
      return
    }
    const target = active && active.endsWith('.tex') && loaded.current.get(active)?.text ? active : undefined
    if (target) setInsert({ path: target, text: `\\cite{${c.key}}` })
    const where = c.added ? t('Added to {path}', { path: c.path }) : t('Already in {path}', { path: c.path })
    toast.success(target ? t('{where} · \\cite{{key}} inserted', { where, key: c.key }) : t('{where} as {key}', { where, key: c.key }), {
      description: c.needs_setup
        ? t('No .tex file loads a bibliography yet: add \\bibliography{{base}} (or \\addbibresource{{path}}).', {
            base: c.path.replace(/\.bib$/, ''),
            path: c.path,
          })
        : undefined,
    })
  }

  function closeOthers(keep: string | null) {
    for (const p of tabs) if (p !== keep) autosave.flush(p)
    forget(tabs.filter((p) => p !== keep))
  }

  const tabMenu = (p: string): MenuEntry[] => [
    { label: t('Close'), icon: X, shortcut: `${mod}W`, onSelect: () => close(p) },
    { label: t('Close others'), onSelect: () => closeOthers(p), disabled: tabs.length < 2 },
    { label: t('Close all'), onSelect: () => closeOthers(null) },
    '-',
    ...(p.endsWith('.tex') && p !== project?.main_file ? [{ label: t('Set as main file'), icon: Star, onSelect: () => setMainFile(p) }] : []),
    { label: t('Copy path'), icon: Copy, onSelect: () => navigator.clipboard.writeText(p) },
  ]

  // "Fix with AI" on compile problems: a new chat asking for the fix (and a check compile).
  function fixWithAI(ps: Problem[]) {
    const lines = ps.map((p) => `- ${p.file ? `${p.file}${p.line ? `:${p.line}` : ''}: ` : ''}${p.severity}: ${p.message}`)
    const text =
      ps.length === 1
        ? t('Fix this problem from the last compile, then compile to check it is gone:\n{problems}', { problems: lines[0] })
        : t('Fix these problems from the last compile, then compile to check they are gone:\n{problems}', { problems: lines.join('\n') })
    setChatId('')
    setChatOpen(true)
    setAiRequest((r) => ({ text, n: (r?.n ?? 0) + 1 }))
  }

  async function jumpTo(path: string, line: number) {
    await open(path)
    setJump((j) => ({ path, line, n: (j?.n ?? 0) + 1 }))
  }

  async function setMainFile(path: string) {
    await run(async () => {
      setProject(await api<Project>(`/api/projects/${id}`, { method: 'PATCH', body: { main_file: path } }))
      toast.success(t('Main file: {path}', { path }))
    })
  }

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn()
    } catch (e) {
      toast.error(errorText(e))
    }
  }

  async function open(path: string) {
    if (!loaded.current.has(path)) {
      try {
        loaded.current.set(path, await readFile(id, path))
      } catch (e) {
        toast.error(errorText(e))
        return
      }
    }
    setReview(null)
    setTabs((t) => (t.includes(path) ? t : [...t, path]))
    setActive(path)
  }

  // Closing never cancels a pending save: the autosaver finishes it on its own.
  function forget(paths: string[]) {
    for (const p of paths) {
      loaded.current.delete(p)
      states.current.delete(p)
    }
    const next = tabs.filter((p) => !paths.includes(p))
    setTabs(next)
    if (active && paths.includes(active)) setActive(next[Math.min(tabs.indexOf(active), next.length - 1)])
  }

  function close(path: string) {
    autosave.flush(path)
    forget([path])
  }

  function askFor(a: Ask) {
    setAskValue(a.value)
    setAsk(a)
  }

  function create(dir: string, kind: 'file' | 'folder') {
    askFor({
      title: kind === 'file' ? t('New file') : t('New folder'),
      description: t('Use slashes for subfolders, e.g. chapters/intro.tex'),
      value: dir ? dir + '/' : '',
      action: t('Create'),
      onSubmit: async (path) => {
        if (!path || path.endsWith('/')) throw new Error(t('Enter a name.'))
        if (files.some((f) => f.path === path)) throw new Error(t('{path} already exists', { path }))
        if (kind === 'file') await writeFile(id, path, '')
        else await mkdir(id, path)
        await loadFiles()
        if (kind === 'file') await open(path)
      },
    })
  }

  function rename(from: string) {
    askFor({
      title: t('Rename or move'),
      description: t('Change the path to move it to another folder.'),
      value: from,
      action: t('Rename'),
      onSubmit: async (to) => {
        if (!to || to === from) return
        const affected = tabs.filter((p) => under(p, from))
        await Promise.all(affected.map((p) => autosave.flush(p))) // save under the old name first
        await move(id, from, to)
        for (const p of affected) {
          const q = moved(p, from, to)
          autosave.rename(p, q)
          for (const m of [loaded.current, states.current] as Map<string, unknown>[]) {
            if (m.has(p)) {
              m.set(q, m.get(p))
              m.delete(p)
            }
          }
        }
        setTabs((t) => t.map((p) => moved(p, from, to)))
        setActive((a) => (a ? moved(a, from, to) : a))
        await loadFiles()
      },
    })
  }

  function remove(path: string, dir: boolean) {
    setConfirm({
      title: t('Delete {path}?', { path }),
      description: dir ? t('The folder and everything in it will be deleted. It stays in the project history.') : t('It stays in the project history.'),
      action: t('Delete'),
      onConfirm: async () => {
        const affected = tabs.filter((p) => under(p, path))
        affected.forEach((p) => autosave.drop(p))
        await removeFile(id, path)
        forget(affected)
        await loadFiles()
      },
    })
  }

  async function upload(dir: string, list: File[]) {
    const target = (f: File) => (dir ? `${dir}/${f.name}` : f.name)
    const write = async () => {
      for (const f of list) {
        autosave.drop(target(f))
        forget([target(f)])
        await writeFile(id, target(f), f)
      }
      await loadFiles()
      toast.success(list.length === 1 ? t('Uploaded {path}', { path: target(list[0]) }) : t('Uploaded {n} files', { n: list.length }))
    }
    const existing = list.filter((f) => files.some((e) => e.path === target(f)))
    if (existing.length === 0) return run(write)
    setConfirm({
      title: existing.length === 1 ? t('Replace {path}?', { path: target(existing[0]) }) : t('Replace {n} files?', { n: existing.length }),
      description: t('The current versions stay in the project history.'),
      action: t('Replace'),
      onConfirm: write,
    })
  }

  function toggleSidebar() {
    const p = sidebar.current
    if (!p) return
    if (p.isCollapsed()) p.expand()
    else p.collapse()
  }

  useCommands(() => [
    { id: 'ws:compile', group: 'Actions', label: t('Compile'), icon: Play, shortcut: `${mod}↵`, run: compile },
    {
      id: 'ws:chat',
      group: 'Actions',
      label: t('New chat'),
      icon: MessageSquarePlus,
      keywords: 'assistant ai ask',
      run: () => {
        setChatId('')
        setChatOpen(true)
        setChatFocus((n) => n + 1)
      },
    },
    {
      id: 'ws:assistant',
      group: 'Actions',
      label: chatOpen ? t('Collapse the assistant') : t('Open the assistant'),
      icon: Sparkles,
      keywords: 'chat ai',
      run: () => setChatOpen(!chatOpen),
    },
    { id: 'ws:newfile', group: 'Actions', label: t('New file'), icon: FileText, run: () => create('', 'file') },
    { id: 'ws:newfolder', group: 'Actions', label: t('New folder'), icon: FolderPlus, run: () => create('', 'folder') },
    { id: 'ws:upload', group: 'Actions', label: t('Upload files'), icon: Upload, run: () => uploadInput.current?.click() },
    { id: 'ws:git', group: 'Actions', label: t('Git remote…'), icon: GitBranch, keywords: 'github remote sync push pull', run: () => setGitOpen(true) },
    { id: 'ws:push', group: 'Actions', label: t('Push to the remote'), icon: ArrowUpFromLine, keywords: 'git github sync upload', run: () => sync('push') },
    { id: 'ws:pull', group: 'Actions', label: t('Pull from the remote'), icon: ArrowDownToLine, keywords: 'git github sync download', run: () => sync('pull') },
    {
      id: 'ws:history',
      group: 'Actions',
      label: t('Project history'),
      icon: HistoryIcon,
      keywords: 'versions restore undo commits',
      run: () => setHistoryOpen(true),
    },
    {
      id: 'ws:cite',
      group: 'Actions',
      label: t('Add a reference…'),
      icon: BookMarked,
      keywords: 'bibliography bibtex reference paper',
      run: () => setCiting({ target: null, suggest: false }),
    },
    { id: 'ws:sidebar', group: 'Actions', label: sidebarCollapsed ? t('Show the sidebar') : t('Hide the sidebar'), icon: PanelLeft, run: toggleSidebar },
    ...files
      .filter((f) => !f.dir)
      .map((f) => {
        const slash = f.path.lastIndexOf('/')
        return {
          id: `file:${f.path}`,
          group: 'Files',
          label: f.path.slice(slash + 1),
          hint: slash > 0 ? f.path.slice(0, slash) : undefined,
          icon: File,
          keywords: f.path,
          run: () => open(f.path),
        }
      }),
    ...chats.map((c) => ({
      id: `chat:${c.id}`,
      group: 'Chats',
      label: c.title,
      hint: ago(c.updated_at),
      icon: MessageSquare,
      run: () => {
        setChatId(c.id)
        setChatOpen(true)
      },
    })),
    ...projects
      .filter((p) => p.id !== id)
      .map((p) => ({ id: `project:${p.id}`, group: 'Projects', label: p.name, icon: FolderOpen, run: () => navigate(`/p/${p.id}`) })),
  ])

  const aiReady = !!status?.ai_ready
  const docked = prefs.chatDocked
  const assistant = (asPanel: boolean) => (
    <Assistant
      projectId={id}
      status={status}
      chats={chats}
      chatId={chatId}
      onChatId={setChatId}
      onChatsChanged={loadChats}
      getContext={editorContext}
      contextLabel={active && current?.text ? `${active.split('/').pop()}${selLines ? ` · L${selLines}` : ''}` : undefined}
      beforeSend={() => autosave.flushAll()}
      onReview={(t) => {
        setReview(t)
        setChatOpen(false) // the diff takes the editor
      }}
      reviewing={review?.id}
      turnUpdate={turnUpdate}
      open={chatOpen}
      onOpen={setChatOpen}
      focus={chatFocus}
      request={aiRequest}
      docked={asPanel}
    />
  )

  const current = active ? loaded.current.get(active) : undefined
  const texFiles = files.filter((f) => !f.dir && f.path.toLowerCase().endsWith('.tex')).map((f) => f.path)
  const saving = active ? saveState[statuses[active] ?? 'saved'] : null

  return (
    <div className="h-full p-2">
      <ResizablePanelGroup orientation="horizontal">
        <ResizablePanel
          id="sidebar"
          panelRef={sidebar}
          defaultSize="248px"
          minSize="190px"
          maxSize="420px"
          collapsible
          collapsedSize={0}
          onResize={(size) => setSidebarCollapsed(size.inPixels < 8)}
        >
          <aside className="flex h-full min-w-0 flex-col pr-1">
            <div className="flex h-11 items-center gap-1 px-1">
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Link
                      to="/"
                      aria-label={t('All projects')}
                      className="shrink-0 rounded-md p-1 transition-transform duration-150 ease-out active:scale-[0.94]"
                    />
                  }
                >
                  <img src="/logo.svg" alt="" className="size-6" style={{ viewTransitionName: 'logo' }} />
                </TooltipTrigger>
                <TooltipContent>{t('All projects')}</TooltipContent>
              </Tooltip>
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="ghost" size="sm" className="min-w-0 flex-1 justify-start gap-1.5 px-2" />}>
                  <span className="truncate font-semibold" style={{ viewTransitionName: 'project-title' }}>
                    {project?.name ?? '…'}
                  </span>
                  <ChevronDown className="opacity-50" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-64">
                  <DropdownMenuItem render={<Link to="/" />}>
                    <ChevronLeft /> {t('All projects')}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => setGitOpen(true)}>
                    <GitBranch /> {t('Git remote…')}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => setHistoryOpen(true)}>
                    <HistoryIcon /> {t('History')}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => setCiting({ target: null, suggest: false })}>
                    <BookMarked /> {t('Add a reference…')}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{t('Recent')}</DropdownMenuLabel>
                    {projects.slice(0, 8).map((p) => (
                      <DropdownMenuItem key={p.id} render={<Link to={`/p/${p.id}`} />}>
                        <span className="truncate">{p.name}</span>
                        {p.id === id && <Check className="ml-auto" />}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
              <Tooltip>
                <TooltipTrigger
                  render={<Button variant="ghost" size="icon-sm" onClick={toggleSidebar} aria-label={t('Hide sidebar')} className="text-muted-foreground" />}
                >
                  <PanelLeft />
                </TooltipTrigger>
                <TooltipContent>{t('Hide sidebar')}</TooltipContent>
              </Tooltip>
            </div>

            <div className="flex items-center gap-1 px-2 pt-1 pb-2">
              <Segmented
                value={sideTab}
                onChange={(v) => setSideTab(v as 'files' | 'chats')}
                options={[
                  { value: 'files', label: t('Files') },
                  { value: 'chats', label: t('Chats') },
                ]}
              />
              <span className="flex-1" />
              {sideTab === 'files' && (
                <DropdownMenu>
                  <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t('Add')} className="text-muted-foreground" />}>
                    <Plus />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="w-44">
                    <DropdownMenuItem onClick={() => create('', 'file')}>
                      <FileText /> {t('New file')}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => create('', 'folder')}>
                      <FolderPlus /> {t('New folder')}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => uploadInput.current?.click()}>
                      <Upload /> {t('Upload files…')}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
              <input
                ref={uploadInput}
                type="file"
                multiple
                hidden
                onChange={(e) => {
                  upload('', [...(e.target.files ?? [])])
                  e.target.value = ''
                }}
              />
            </div>

            {sideTab === 'files' ? (
              <FileTree
                files={files}
                active={review ? undefined : active}
                dirty={(p) => autosave.isDirty(p)}
                onOpen={open}
                onNew={create}
                onRename={rename}
                onDelete={remove}
                onUpload={upload}
                onUploadClick={() => uploadInput.current?.click()}
                onSetMain={setMainFile}
                onDownload={(p) => run(() => download(fileURL(id, p), p.split('/').pop()!))}
                mainFile={project?.main_file}
              />
            ) : (
              <div className="min-h-0 flex-1 overflow-auto px-2">
                <Button
                  variant="ghost"
                  size="sm"
                  className="mb-1 w-full justify-start text-muted-foreground"
                  onClick={() => {
                    setChatId('')
                    setChatOpen(true)
                    setChatFocus((n) => n + 1)
                  }}
                >
                  <Plus /> {t('New chat')}
                </Button>
                {chats.map((c) => (
                  <button
                    key={c.id}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors duration-100',
                      c.id === chatId ? 'bg-foreground/[0.07]' : 'text-foreground/80 hover:bg-foreground/[0.04]',
                    )}
                    onClick={() => {
                      setChatId(c.id)
                      setChatOpen(true)
                    }}
                  >
                    <MessageSquare className="size-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate">{c.title}</span>
                    <span className="text-xs text-muted-foreground">{ago(c.updated_at)}</span>
                  </button>
                ))}
                {chats.length === 0 && <p className="px-2 py-3 text-xs text-muted-foreground">{t('No chats yet.')}</p>}
              </div>
            )}

            <div className="pt-1">
              <AccountMenu status={status} />
            </div>
          </aside>
        </ResizablePanel>
        <ResizableHandle className={cn('w-2 bg-transparent', sidebarCollapsed && 'w-0')} />

        <ResizablePanel id="editor" minSize="320px">
          <main className="flex h-full min-w-0 flex-col overflow-hidden rounded-xl border bg-card shadow-xs">
            <div className="flex h-11 shrink-0 items-center gap-1.5 border-b px-2">
              {sidebarCollapsed && (
                <Tooltip>
                  <TooltipTrigger
                    render={<Button variant="ghost" size="icon-sm" onClick={toggleSidebar} aria-label={t('Show sidebar')} className="text-muted-foreground" />}
                  >
                    <PanelLeft />
                  </TooltipTrigger>
                  <TooltipContent>{t('Show sidebar')}</TooltipContent>
                </Tooltip>
              )}
              <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none]" role="tablist">
                {tabs.map((p) => {
                  const on = p === active && !review
                  const dirty = statuses[p] && statuses[p] !== 'saved'
                  return (
                    <WithContextMenu key={p} items={() => tabMenu(p)}>
                      <div
                        data-path={p}
                        role="tab"
                        aria-selected={on}
                        className={cn(
                          'group/tab flex h-7 shrink-0 items-center rounded-md border text-xs transition-colors duration-100',
                          on
                            ? 'border-border bg-background text-foreground shadow-xs'
                            : 'border-transparent text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                        )}
                      >
                        <button
                          className="flex items-center gap-1.5 py-1 pr-1 pl-2.5"
                          onClick={() => {
                            setReview(null)
                            setActive(p)
                          }}
                          onAuxClick={(e) => e.button === 1 && close(p)}
                          title={p}
                        >
                          <FileText className="size-3.5 opacity-70" />
                          {p.split('/').pop()}
                        </button>
                        <button
                          className="relative mr-1 flex size-4 items-center justify-center rounded-sm transition-colors duration-100 hover:bg-accent"
                          onClick={() => close(p)}
                          aria-label={t('Close {path}', { path: p })}
                        >
                          {/* unsaved: a dot that turns into × on hover */}
                          {dirty && <span className="absolute size-1.5 rounded-full bg-warning group-hover/tab:hidden" />}
                          <X
                            className={cn(
                              'size-3',
                              dirty ? 'hidden group-hover/tab:block' : 'opacity-0 group-hover/tab:opacity-100',
                              on && !dirty && 'opacity-60',
                            )}
                          />
                        </button>
                      </div>
                    </WithContextMenu>
                  )
                })}
                {review && (
                  <div className="flex h-7 shrink-0 items-center gap-1.5 rounded-md border bg-background px-2.5 text-xs shadow-xs">
                    <Check className="size-3.5" /> {t('Review')}
                  </div>
                )}
              </div>
              <div className="flex shrink-0 items-center">
                <Tooltip>
                  <TooltipTrigger render={<Button size="sm" className="rounded-r-none" onClick={compile} disabled={compiling || !project?.main_file} />}>
                    {compiling ? <Loader2 className="animate-spin" /> : <Play className="fill-current" />}
                    {t('Compile')}
                  </TooltipTrigger>
                  <TooltipContent>
                    {t('Compile')} <kbd className="ml-1 opacity-70">{mod}↵</kbd>
                  </TooltipContent>
                </Tooltip>
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={<Button size="icon-sm" className="rounded-l-none border-l border-primary-foreground/20" aria-label={t('Compile options')} />}
                  >
                    <ChevronDown />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="max-h-80 w-64">
                    <DropdownMenuGroup>
                      <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{t('Main file')}</DropdownMenuLabel>
                      {texFiles.map((f) => (
                        <DropdownMenuItem key={f} onClick={() => setMainFile(f)}>
                          <span className="truncate font-mono text-xs">{f}</span>
                          <Check className={cn('ml-auto', project?.main_file !== f && 'invisible')} />
                        </DropdownMenuItem>
                      ))}
                      {texFiles.length === 0 && <p className="px-2 py-1.5 text-xs text-muted-foreground">{t('No .tex files yet.')}</p>}
                    </DropdownMenuGroup>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            </div>

            <div className="relative min-h-0 flex-1">
              {review && <Review turn={review} onAccept={acceptTurn} onReject={rejectTurn} onClose={() => setReview(null)} />}
              {!review && active && current?.text && (
                <Editor
                  key={editorKey}
                  path={active}
                  states={states.current}
                  initial={(p) => {
                    const f = loaded.current.get(p)
                    return f?.text ? f.content : ''
                  }}
                  onChange={(p, c) => autosave.change(p, c)}
                  onSave={() => active && autosave.flush(active)}
                  onCompile={compile}
                  onSync={syncToPDF}
                  onSelect={setSelLines}
                  insert={insert}
                  onInserted={() => setInsert(undefined)}
                  onCite={(target, suggest) => setCiting({ target, suggest })}
                  proposal={proposal}
                  onProposed={(ok) => {
                    setProposal(undefined)
                    if (!ok) toast.error(t('The text changed in the meantime: the citation was added to the bibliography only.'))
                  }}
                  quick={aiReady}
                  jump={jump}
                />
              )}
              {!review && active && current && !current.text && <BinaryView file={current} path={active} />}
              {!review && !active && <EmptyEditor />}
              {!docked && assistant(false)}
            </div>

            {!review && saving && current?.text && (
              <div className="flex h-7 shrink-0 items-center gap-1.5 border-t px-3 text-xs text-muted-foreground">
                <span className={cn('size-1.5 rounded-full', saving.dot)} />
                {saving.text}
                <span className="flex-1" />
                <span className="hidden sm:inline">{t('{mod}J sync to PDF · {mod}↵ compile', { mod })}</span>
              </div>
            )}
          </main>
        </ResizablePanel>
        <ResizableHandle className="w-2 bg-transparent" />

        <ResizablePanel id="right" defaultSize="42%" minSize="320px">
          <section className="flex h-full min-w-0 flex-col overflow-hidden rounded-xl border bg-card shadow-xs">
            <PdfViewer
              projectId={id}
              version={pdfVersion}
              highlight={highlight}
              onInverse={syncToSource}
              toolbar={
                <CompileStatus
                  projectId={id}
                  compiling={compiling}
                  result={result}
                  error={compileError}
                  onJump={jumpTo}
                  onFix={aiReady ? fixWithAI : undefined}
                />
              }
            />
          </section>
        </ResizablePanel>
        {docked && (
          <>
            <ResizableHandle className="w-2 bg-transparent" />
            <ResizablePanel id="chat" defaultSize="380px" minSize="300px" maxSize="50%">
              {assistant(true)}
            </ResizablePanel>
          </>
        )}
      </ResizablePanelGroup>

      <Dialog open={!!ask} onOpenChange={(o) => !o && setAsk(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{ask?.title}</DialogTitle>
            {ask?.description && <DialogDescription>{ask.description}</DialogDescription>}
          </DialogHeader>
          <form
            onSubmit={async (e) => {
              e.preventDefault()
              if (!ask) return
              try {
                await ask.onSubmit(askValue.trim())
                setAsk(null)
              } catch (err) {
                toast.error(errorText(err))
              }
            }}
          >
            <Input autoFocus value={askValue} onChange={(e) => setAskValue(e.target.value)} className="font-mono text-sm" aria-label={ask?.title} />
            <DialogFooter className="mt-4">
              <Button type="button" variant="ghost" onClick={() => setAsk(null)}>
                {t('Cancel')}
              </Button>
              <Button type="submit">{ask?.action}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <GitDialog
        projectId={id}
        projectName={project?.name ?? ''}
        open={gitOpen}
        onClose={() => setGitOpen(false)}
        beforeSync={() => autosave.flushAll()}
        onPulled={pulled}
      />
      <History
        projectId={id}
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        beforeRestore={() => autosave.flushAll()}
        onRestored={async () => {
          await reloadFromDisk(tabs)
          setReview(null)
        }}
      />
      <CiteDialog
        projectId={id}
        open={!!citing}
        target={citing?.target ?? null}
        suggest={citing?.suggest ?? false}
        onClose={() => setCiting(null)}
        onCited={cited}
      />

      <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirm?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant={confirm?.action === t('Delete') ? 'destructive' : 'default'}
              onClick={() => {
                const c = confirm
                setConfirm(null)
                if (c) run(c.onConfirm)
              }}
            >
              {confirm?.action}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

// Two-option switch with a sliding thumb (transform only: cheap and interruptible).
function Segmented({ value, onChange, options }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[] }) {
  const index = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  )
  return (
    <div className="relative grid shrink-0 grid-cols-2 rounded-lg bg-foreground/[0.06] p-0.5 text-xs" role="tablist">
      <span
        className="absolute inset-y-0.5 left-0.5 w-[calc(50%-2px)] rounded-md bg-background shadow-xs transition-transform duration-200 ease-out"
        style={{ transform: `translateX(${index * 100}%)` }}
        aria-hidden
      />
      {options.map((o) => (
        <button
          key={o.value}
          role="tab"
          aria-selected={o.value === value}
          className={cn(
            'relative px-3 py-1 font-medium transition-colors duration-150',
            o.value === value ? 'text-foreground' : 'text-muted-foreground hover:text-foreground',
          )}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function EmptyEditor() {
  const rows = editorShortcuts.slice(0, 4)
  return (
    <div className="enter flex h-full flex-col items-center justify-center gap-5 pb-16 text-center">
      <div>
        <p className="text-sm font-medium">{t('Open a file from the sidebar')}</p>
        <p className="mt-1 text-sm text-muted-foreground">{t('Edits save automatically.')}</p>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-left text-xs">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt>
              <kbd className="rounded border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">{k}</kbd>
            </dt>
            <dd className="text-muted-foreground">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function BinaryView({ file, path }: { file: Extract<OpenedFile, { text: false }>; path: string }) {
  const [url, setUrl] = useState('')
  useEffect(() => {
    const u = URL.createObjectURL(file.blob)
    setUrl(u)
    return () => URL.revokeObjectURL(u)
  }, [file])
  if (file.type.startsWith('image/')) {
    return (
      <div className="flex h-full items-center justify-center overflow-auto bg-[conic-gradient(var(--muted)_25%,transparent_0_50%,var(--muted)_0_75%,transparent_0)] bg-[length:16px_16px] p-6">
        {url && <img src={url} alt={path} className="enter max-h-full max-w-full rounded shadow-md" />}
      </div>
    )
  }
  return (
    <div className="flex h-full flex-col items-center justify-center gap-1 text-sm text-muted-foreground">
      <p className="font-mono text-foreground">{path}</p>
      <p>
        {file.type || t('Binary file')} · {(file.size / 1024).toFixed(1)} KB · {t('read-only')}
      </p>
    </div>
  )
}
