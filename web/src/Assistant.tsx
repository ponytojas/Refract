import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import {
  ArrowUp,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Copy,
  FileDiff,
  FileText,
  MessageSquarePlus,
  Minimize2,
  PanelBottom,
  PanelRight,
  Sparkles,
  Square,
  X,
} from 'lucide-react'
import { api, describe, errorText, stream, type ApiError, type ChatGPTStatus, type Model } from './api'
import ModelPicker from './ModelPicker'
import { resolveChoice } from '@/lib/models'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Conversation, ConversationContent } from '@/components/ui/chatcn/conversation'
import { ScrollToBottom } from '@/components/ui/chatcn/scroll-to-bottom'
import { ShimmerText } from '@/components/ui/chatcn/shimmer-text'
import { ThinkingIndicator } from '@/components/ui/chatcn/thinking-indicator'
import PlanBadge from './PlanBadge'
import Markdown from '@/components/Markdown'
import { openSettings, setPref, usePrefs } from '@/lib/prefs'
import { ago } from '@/lib/time'
import { cn } from '@/lib/utils'
import { t, tn } from '@/lib/i18n'

export type Turn = {
  id: string
  message: string
  reply: string
  reasoning?: string // the model's reasoning summary, when it gives one
  activity: string[]
  status: 'running' | 'review' | 'done' | 'accepted' | 'rejected' | 'failed' | 'stopped'
  error?: ApiError
  files: { path: string; status: string }[]
  commits: string[]
}
export type Chat = { id: string; title: string; model: string; effort: string; updated_at: string }
export type EditorContext = { open_file: string; selection: string; from_line: number; to_line: number }

type Props = {
  projectId: string
  status: ChatGPTStatus | null
  chats: Chat[]
  chatId: string
  onChatId: (id: string) => void
  onChatsChanged: () => void
  getContext: () => EditorContext
  contextLabel?: string // what getContext will send, shown as a chip: "main.tex · L3–9"
  beforeSend: () => Promise<void> // flush autosave: the turn starts from what is on disk
  onReview: (turn: Turn) => void
  reviewing?: string
  turnUpdate?: Turn // a turn changed by the review view
  open: boolean // the chat sheet over the editor; collapsed it is an input bar or a bubble
  docked?: boolean // rendered in a side panel: always open, no bar or bubble
  onOpen: (open: boolean) => void
  focus?: number // bump to focus the input
  request?: { text: string; n: number } // send this (n changes on every request)
  readOnly?: boolean // a project in the trash: nothing can be sent; the draft is kept
}

const finished: Partial<Record<Turn['status'], { label: string; tone: string }>> = {
  done: { label: t('No changes'), tone: 'text-muted-foreground' },
  accepted: { label: t('Changes applied'), tone: 'text-success' },
  rejected: { label: t('Changes rejected'), tone: 'text-muted-foreground' },
  stopped: { label: t('Stopped'), tone: 'text-muted-foreground' },
}

const suggestions = [
  t('Fix the compile errors'),
  t('Check the grammar of the open file'),
  t('Make the abstract more concise'),
]
const MIN_HEIGHT = 260

// The assistant floats over the editor (Prism-style): an input bar, a bubble, or a resizable sheet.
export default function Assistant(p: Props) {
  const prefs = usePrefs()
  const [turns, setTurns] = useState<Turn[]>([])
  const [draft, setDraft] = useState('')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [height, setHeight] = useState<number | null>(null) // while dragging; prefs keep the last one
  const [models, setModels] = useState<Model[]>([])
  const abort = useRef<AbortController | null>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  const overlay = useRef<HTMLDivElement>(null)
  const created = useRef('') // chat created by send(): its (empty) history must not overwrite the live turn

  useEffect(() => {
    if (!p.chatId) return setTurns([])
    if (p.chatId === created.current) return
    api<Turn[]>(`/api/chats/${p.chatId}/turns`)
      .then(setTurns)
      .catch((e) => setError(errorText(e)))
  }, [p.chatId])

  useEffect(() => {
    const u = p.turnUpdate
    if (u) setTurns((ts) => ts.map((t) => (t.id === u.id ? u : t)))
  }, [p.turnUpdate])

  useEffect(() => {
    if (p.focus) input.current?.focus()
  }, [p.focus])

  useEffect(() => {
    if (!p.request) return
    if (running) setError(t('The assistant is busy with another request.'))
    else send(p.request.text)
  }, [p.request?.n])

  const usable = !!p.status?.ai_ready
  useEffect(() => {
    if (!usable) return
    const load = () =>
      api<Model[]>('/api/models')
        .then(setModels)
        .catch(() => {}) // the server falls back to its default model
    load()
    window.addEventListener('refract:models', load)
    return () => window.removeEventListener('refract:models', load)
  }, [usable])
  const choice = resolveChoice(models, prefs.model, prefs.effort)

  // Each chat continues with the model it last used; new chats take the last pick.
  const chat = p.chats.find((c) => c.id === p.chatId)
  useEffect(() => {
    if (!chat?.model) return
    setPref('model', chat.model)
    if (chat.effort) setPref('effort', chat.effort)
  }, [p.chatId, chat?.model, chat?.effort])

  const open = p.open || !!p.docked
  const bubble = !open && prefs.chatBubble

  // Grow the input with its content (up to a cap).
  useEffect(() => {
    const el = input.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [draft, open, bubble])

  async function newChat() {
    if (running || p.readOnly) return
    p.onChatId('')
    setError('')
    p.onOpen(true)
    input.current?.focus()
  }

  async function send(text = draft) {
    const message = text.trim()
    if (!message || running || p.readOnly) return
    setError('')
    setRunning(true)
    p.onOpen(true)
    try {
      await p.beforeSend()
      let id = p.chatId
      if (!id) {
        const c = await api<Chat>(`/api/projects/${p.projectId}/chats`, { method: 'POST' })
        created.current = c.id
        p.onChatId(c.id)
        id = c.id
      }
      const pending: Turn = {
        id: 'pending',
        message,
        reply: '',
        activity: [],
        status: 'running',
        files: [],
        commits: [],
      }
      setTurns((ts) => [...ts, pending])
      setDraft('')
      const update = (fn: (t: Turn) => Turn) => setTurns((ts) => ts.map((t) => (t.id === 'pending' ? fn(t) : t)))
      abort.current = new AbortController()
      await stream(
        `/api/chats/${id}/turns`,
        // Unresolved (models not loaded yet): send the stored pick, the server validates it.
        {
          message,
          context: p.getContext(),
          choice: choice.model
            ? { model: choice.model.slug, effort: choice.effort }
            : { model: prefs.model, effort: prefs.effort },
        },
        (ev) => {
          if (ev.type === 'text') update((t) => ({ ...t, reply: t.reply + ev.text }))
          if (ev.type === 'reasoning') update((t) => ({ ...t, reasoning: (t.reasoning ?? '') + ev.text }))
          if (ev.type === 'activity') update((t) => ({ ...t, activity: [...t.activity, ev.activity] }))
          if (ev.type === 'turn') {
            setTurns((ts) => [...ts.filter((t) => t.id !== 'pending' && t.id !== ev.turn.id), ev.turn])
            if (ev.turn.status === 'review' && !p.readOnly) p.onReview(ev.turn)
          }
          if (ev.type === 'error') update((t) => ({ ...t, status: 'failed', error: ev.error }))
        },
        abort.current.signal,
      )
    } catch (e) {
      if ((e as Error).name === 'AbortError') {
        setTurns((ts) => ts.map((t) => (t.id === 'pending' ? { ...t, status: 'stopped' } : t)))
      } else setError(errorText(e))
    } finally {
      setRunning(false)
      p.onChatsChanged() // titles change after the first message
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      e.stopPropagation() // not the global Cmd+Enter compile
      send()
    }
  }

  // Esc collapses the sheet; menus portalled out of it handle their own Esc.
  function onCardKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && p.open && !p.docked && e.currentTarget.contains(e.target as Node)) {
      e.preventDefault()
      p.onOpen(false)
    }
  }

  // Drag the top edge to resize the sheet. Pointer capture keeps the drag when leaving the handle.
  function startResize(e: PointerEvent) {
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  function resize(e: PointerEvent) {
    if (!e.currentTarget.hasPointerCapture(e.pointerId) || !overlay.current) return
    const r = overlay.current.getBoundingClientRect()
    setHeight(Math.max(MIN_HEIGHT, Math.min(r.bottom - 12 - e.clientY, r.height - 24)))
  }
  function endResize() {
    if (height !== null) setPref('chatHeight', Math.round(height))
    setHeight(null)
  }

  const plan = p.status?.status
  const notice = error || (p.status && !p.status.ai_ready ? describe({ kind: 'not_signed_in', message: '' }) : '')
  const title = p.chats.find((c) => c.id === p.chatId)?.title ?? t('New chat')
  const last = turns[turns.length - 1]
  const pendingReview = last?.status === 'review' ? last : undefined
  const sheetHeight = height ?? prefs.chatHeight

  if (bubble) {
    return (
      <div className="pointer-events-none absolute inset-0 z-20 flex items-end justify-end p-4">
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                onClick={() => p.onOpen(true)}
                aria-label={t('Open assistant')}
                className="pointer-events-auto relative flex size-11 origin-bottom-right items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg transition-[scale,opacity] duration-200 ease-out hover:scale-105 active:scale-95 starting:scale-90 starting:opacity-0"
              />
            }
          >
            <Sparkles className="size-5" />
            {(running || pendingReview) && (
              <span
                className={cn(
                  'absolute top-0 right-0 size-3 rounded-full border-2 border-card',
                  running ? 'animate-pulse bg-muted-foreground' : 'bg-warning',
                )}
              />
            )}
          </TooltipTrigger>
          <TooltipContent side="left">
            {running ? t('Assistant is working…') : pendingReview ? t('Changes ready to review') : t('Assistant')}
          </TooltipContent>
        </Tooltip>
      </div>
    )
  }

  return (
    <div
      ref={overlay}
      className={
        p.docked
          ? 'flex h-full min-h-0 flex-col'
          : 'pointer-events-none absolute inset-0 z-20 flex flex-col justify-end p-3'
      }
    >
      <div
        onKeyDown={onCardKey}
        style={open && !p.docked ? { height: sheetHeight ? sheetHeight : '55%' } : undefined}
        className={cn(
          'relative flex w-full flex-col',
          p.docked
            ? 'h-full min-h-0 overflow-hidden rounded-xl border bg-card shadow-xs'
            : [
                'pointer-events-auto mx-auto max-h-full rounded-2xl border bg-card/95 shadow-lg backdrop-blur-md',
                // The bar appears from the bubble's corner; the sheet just grows (its content fades in).
                'origin-bottom-right transition-[scale,opacity] duration-200 ease-out starting:scale-95 starting:opacity-0',
                open ? 'min-h-[260px]' : 'max-w-3xl',
              ],
        )}
      >
        {open && (
          <>
            {!p.docked && (
              <div
                role="separator"
                aria-orientation="horizontal"
                aria-label={t('Resize assistant')}
                onPointerDown={startResize}
                onPointerMove={resize}
                onPointerUp={endResize}
                onPointerCancel={endResize}
                onDoubleClick={() => setPref('chatHeight', 0)}
                className="group/handle absolute inset-x-0 -top-1.5 z-10 flex h-4 cursor-ns-resize touch-none justify-center pt-2"
              >
                <span className="h-1 w-10 rounded-full bg-border transition-[background-color,width] duration-150 group-hover/handle:w-14 group-hover/handle:bg-muted-foreground/50" />
              </div>
            )}
            <div
              className={cn(
                'flex h-11 shrink-0 enter items-center gap-1 px-2',
                !p.docked && 'pt-1',
                p.docked && 'border-b',
              )}
            >
              {!p.docked && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground"
                        onClick={() => p.onOpen(false)}
                        aria-label={t('Collapse assistant')}
                      />
                    }
                  >
                    <ChevronDown />
                  </TooltipTrigger>
                  <TooltipContent>
                    {t('Collapse')} <kbd className="ml-1 opacity-70">Esc</kbd>
                  </TooltipContent>
                </Tooltip>
              )}
              <DropdownMenu>
                <DropdownMenuTrigger
                  disabled={running}
                  render={<Button variant="ghost" size="sm" className="min-w-0 gap-1 font-medium" />}
                >
                  <span className="truncate">{title}</span> <ChevronDown className="opacity-50" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="max-h-80 w-72">
                  <DropdownMenuItem onClick={newChat}>
                    <MessageSquarePlus /> {t('New chat')}
                  </DropdownMenuItem>
                  {p.chats.length > 0 && <DropdownMenuSeparator />}
                  <DropdownMenuGroup>
                    {p.chats.length > 0 && (
                      <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
                        {t('Recent chats')}
                      </DropdownMenuLabel>
                    )}
                    {p.chats.map((c) => (
                      <DropdownMenuItem key={c.id} onClick={() => p.onChatId(c.id)}>
                        <span className="truncate">{c.title}</span>
                        <span className="ml-auto shrink-0 text-xs text-muted-foreground">{ago(c.updated_at)}</span>
                        <Check className={cn(c.id !== p.chatId && 'hidden')} />
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
              <span className="flex-1" />
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-muted-foreground"
                      onClick={newChat}
                      disabled={running}
                      aria-label={t('New chat')}
                    />
                  }
                >
                  <MessageSquarePlus />
                </TooltipTrigger>
                <TooltipContent>{t('New chat')}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-muted-foreground"
                      disabled={running || p.readOnly}
                      onClick={() => {
                        setPref('chatDocked', !p.docked)
                        p.onOpen(true)
                      }}
                      aria-label={p.docked ? t('Float over the editor') : t('Dock to the side')}
                    />
                  }
                >
                  {p.docked ? <PanelBottom /> : <PanelRight />}
                </TooltipTrigger>
                <TooltipContent>{p.docked ? t('Float over the editor') : t('Dock to the side')}</TooltipContent>
              </Tooltip>
              {!p.docked && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground"
                        onClick={() => {
                          setPref('chatBubble', !prefs.chatBubble)
                          p.onOpen(false)
                        }}
                        aria-label={prefs.chatBubble ? t('Collapse to input bar') : t('Minimize to bubble')}
                      />
                    }
                  >
                    {prefs.chatBubble ? <PanelBottom /> : <Minimize2 />}
                  </TooltipTrigger>
                  <TooltipContent>
                    {prefs.chatBubble ? t('Collapse to input bar') : t('Minimize to bubble')}
                  </TooltipContent>
                </Tooltip>
              )}
            </div>

            <Conversation className="min-h-0 flex-1 enter">
              <ConversationContent className="max-w-3xl gap-6 px-5 pt-3 pb-4 sm:px-6">
                {turns.length === 0 && (
                  <div className="mx-auto mt-6 max-w-sm text-center">
                    <p className="text-sm font-medium">{t('Ask for a change')}</p>
                    <p className="mt-1 text-sm text-muted-foreground">
                      {t(
                        'The assistant reads your project, edits a copy and proposes a diff. Nothing changes until you accept.',
                      )}
                    </p>
                    <div className="mt-4 flex flex-wrap justify-center gap-1.5">
                      {suggestions.map((s) => (
                        <button
                          key={s}
                          className="rounded-full border px-3 py-1 text-xs text-muted-foreground transition-[color,background-color,scale] duration-150 hover:bg-accent hover:text-foreground active:scale-[0.97]"
                          onClick={() => {
                            setDraft(s)
                            input.current?.focus()
                          }}
                        >
                          {s}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
                {turns.map((t) => (
                  <TurnView
                    key={t.id}
                    turn={t}
                    status={p.status}
                    reviewing={p.reviewing === t.id}
                    onReview={p.readOnly ? undefined : () => p.onReview(t)}
                  />
                ))}
              </ConversationContent>
              <ScrollToBottom />
            </Conversation>
          </>
        )}

        {!open && (running || pendingReview) && (
          <div className="flex enter items-center gap-2 border-b px-4 py-2 text-xs">
            {running ? (
              <ShimmerText className="min-w-0 truncate">
                {last?.activity[last.activity.length - 1] ?? t('Thinking')}
              </ShimmerText>
            ) : (
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <FileDiff className="size-3.5" />{' '}
                {tn(pendingReview!.files.length, '{n} file changed', '{n} files changed')}
              </span>
            )}
            <span className="flex-1" />
            {pendingReview && !running && !p.readOnly && p.reviewing !== pendingReview.id && (
              <Button size="xs" variant="secondary" onClick={() => p.onReview(pendingReview)}>
                {t('Review')}
              </Button>
            )}
            <Button size="xs" variant="ghost" onClick={() => p.onOpen(true)}>
              {t('Open chat')}
            </Button>
          </div>
        )}

        {notice && (
          <p className="flex enter items-center gap-2 px-4 pt-2 text-xs text-destructive">
            <span className="min-w-0 flex-1">{notice}</span>
            {!error && (
              <Button size="xs" variant="ghost" onClick={() => openSettings('assistant')}>
                {t('Open settings')}
              </Button>
            )}
          </p>
        )}

        <div className={cn('shrink-0', open && 'p-2 pt-0')}>
          <div
            className={cn(
              'flex transition-[border-color,box-shadow] duration-150',
              open ? 'flex-col rounded-xl border bg-background focus-within:border-ring' : 'items-end',
            )}
          >
            <textarea
              ref={input}
              rows={1}
              value={draft}
              placeholder={
                p.readOnly
                  ? t('Restore the project to use the assistant.')
                  : open
                    ? t('Ask for a change, or ask about the document…')
                    : t('Ask anything')
              }
              disabled={p.readOnly}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={onKey}
              aria-label={t('Message the assistant')}
              className={cn(
                'block max-h-[200px] min-w-0 flex-1 resize-none bg-transparent text-sm outline-none placeholder:text-muted-foreground',
                open ? 'px-3.5 pt-3 pb-1' : 'px-4 py-3',
              )}
            />
            <div className={cn('flex shrink-0 items-center gap-1', open ? 'px-2 pb-2' : 'py-2 pr-2')}>
              {open && (
                <>
                  {p.contextLabel && (
                    <span
                      className="flex min-w-0 items-center gap-1 rounded-md bg-foreground/[0.05] px-1.5 py-0.5 text-xs text-muted-foreground"
                      title={t('Sent with your message')}
                    >
                      <FileText className="size-3 shrink-0" />
                      <span className="truncate">{p.contextLabel}</span>
                    </span>
                  )}
                  <span className="flex-1" />
                  {plan?.plan_granted && p.status && <PlanBadge manageUsage={p.status.manage_usage} />}
                </>
              )}
              <ModelPicker models={models} model={choice.model} effort={choice.effort} compact={!open} />
              {!open && turns.length > 0 && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground"
                        onClick={() => p.onOpen(true)}
                        aria-label={t('Show chat')}
                      />
                    }
                  >
                    <ChevronUp />
                  </TooltipTrigger>
                  <TooltipContent>{t('Show chat')}</TooltipContent>
                </Tooltip>
              )}
              {!open && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground"
                        onClick={() => setPref('chatBubble', true)}
                        aria-label={t('Minimize to bubble')}
                      />
                    }
                  >
                    <Minimize2 />
                  </TooltipTrigger>
                  <TooltipContent>{t('Minimize to bubble')}</TooltipContent>
                </Tooltip>
              )}
              {running ? (
                <Button
                  size="icon-sm"
                  variant="secondary"
                  className="rounded-full"
                  onClick={() => abort.current?.abort()}
                  aria-label={t('Stop')}
                >
                  <Square className="size-3 fill-current" />
                </Button>
              ) : (
                <Button
                  size="icon-sm"
                  className="rounded-full"
                  onClick={() => send()}
                  disabled={!draft.trim() || p.readOnly}
                  aria-label={t('Send')}
                >
                  <ArrowUp />
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// The model's reasoning summary: shown while it thinks, folded once it answers.
function Reasoning({ text, live }: { text: string; live: boolean }) {
  const [open, setOpen] = useState(false)
  const shown = live || open
  return (
    <div>
      <button
        className="flex items-center gap-1 text-xs text-muted-foreground transition-colors duration-100 hover:text-foreground"
        onClick={() => setOpen(!open)}
        aria-expanded={shown}
      >
        <ChevronRight className={cn('size-3.5 transition-transform duration-150 ease-out', shown && 'rotate-90')} />
        {live ? <ShimmerText className="font-normal">{t('Thinking')}</ShimmerText> : t('Reasoning')}
      </button>
      {shown && (
        <div className="mt-1.5 ml-1.5 max-h-56 enter overflow-y-auto border-l pl-3 text-xs leading-relaxed text-muted-foreground">
          <Markdown text={text} />
        </div>
      )}
    </div>
  )
}

function TurnView({
  turn,
  status,
  reviewing,
  onReview,
}: {
  turn: Turn
  status: ChatGPTStatus | null
  reviewing: boolean
  onReview?: () => void // absent: nothing can be applied (read-only)
}) {
  const [showSteps, setShowSteps] = useState(false)
  const request = turn.message.split('\n\n[Open file:')[0]
  const running = turn.status === 'running'
  const done = finished[turn.status]
  return (
    <div className="flex flex-col gap-3">
      <div
        data-slot="message"
        data-role="user"
        className="ml-auto w-fit max-w-[85%] enter rounded-2xl rounded-br-md bg-secondary px-3.5 py-2 text-sm whitespace-pre-wrap"
      >
        {request}
      </div>

      {turn.activity.length > 0 &&
        (running ? (
          <p className="flex min-w-0 items-center gap-2 text-xs">
            <ShimmerText key={turn.activity.length} className="enter truncate font-mono font-normal">
              {turn.activity[turn.activity.length - 1]}
            </ShimmerText>
          </p>
        ) : (
          <div>
            <button
              className="flex items-center gap-1 text-xs text-muted-foreground transition-colors duration-100 hover:text-foreground"
              onClick={() => setShowSteps(!showSteps)}
              aria-expanded={showSteps}
            >
              <ChevronRight
                className={cn('size-3.5 transition-transform duration-150 ease-out', showSteps && 'rotate-90')}
              />
              {tn(turn.activity.length, 'Worked · {n} step', 'Worked · {n} steps')}
            </button>
            {showSteps && (
              <ul className="mt-1.5 ml-1.5 enter space-y-0.5 border-l pl-3 font-mono text-xs text-muted-foreground">
                {turn.activity.map((a, i) => (
                  <li key={i}>{a}</li>
                ))}
              </ul>
            )}
          </div>
        ))}

      {turn.reasoning?.trim() && <Reasoning text={turn.reasoning} live={running && !turn.reply} />}

      <ThinkingIndicator active={running && turn.activity.length === 0 && !turn.reply && !turn.reasoning?.trim()} />

      {turn.reply && (
        <div className="group/reply">
          <div className="text-sm leading-relaxed">
            <Markdown text={turn.reply} />
          </div>
          {!running && <CopyButton text={turn.reply} />}
        </div>
      )}

      {turn.status === 'review' && (
        <div className="enter rounded-xl border bg-background p-3">
          <div className="flex items-center gap-2 text-sm font-medium">
            <FileDiff className="size-4 text-muted-foreground" />{' '}
            {tn(turn.files.length, '{n} file changed', '{n} files changed')}
          </div>
          <ul className="mt-1.5 space-y-0.5 font-mono text-xs text-muted-foreground">
            {turn.files.slice(0, 5).map((f) => (
              <li key={f.path} className="truncate">
                {f.path}
              </li>
            ))}
            {turn.files.length > 5 && <li>{t('… and {n} more', { n: turn.files.length - 5 })}</li>}
          </ul>
          {onReview && (
            <Button size="sm" className="mt-3 w-full" variant={reviewing ? 'secondary' : 'default'} onClick={onReview}>
              {reviewing ? t('Reviewing…') : t('Review changes')}
            </Button>
          )}
        </div>
      )}

      {done && (
        <p className={cn('flex items-center gap-1.5 text-xs', done.tone)}>
          {turn.status === 'accepted' ? (
            <Check className="size-3.5" />
          ) : turn.status === 'rejected' ? (
            <X className="size-3.5" />
          ) : null}
          {done.label}
        </p>
      )}

      {turn.error && (
        <div className="enter rounded-xl border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          {describe(turn.error)}
          {turn.error.kind === 'usage_limit' && status && (
            <>
              {' '}
              <a className="underline underline-offset-4" href={status.manage_usage} target="_blank" rel="noreferrer">
                {t('Manage usage')}
              </a>
            </>
          )}
        </div>
      )}
    </div>
  )
}

// Copy → check swap, blurred crossfade so the two icons read as one morph.
function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(t)
  }, [copied])
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-xs"
            className="mt-1 text-muted-foreground opacity-60 transition-opacity duration-150 group-hover/reply:opacity-100 focus-visible:opacity-100"
            aria-label={t('Copy reply')}
            onClick={() => navigator.clipboard.writeText(text).then(() => setCopied(true))}
          />
        }
      >
        <span key={String(copied)} className="grid animate-[chatcn-swap_150ms_var(--ease-out)] place-items-center">
          {copied ? <Check /> : <Copy />}
        </span>
      </TooltipTrigger>
      <TooltipContent>{copied ? t('Copied') : t('Copy')}</TooltipContent>
    </Tooltip>
  )
}
