import { useEffect, useRef, useState } from 'react'
import {
  CheckCircle2,
  Code2,
  Copy,
  ExternalLink,
  GitBranch,
  Keyboard,
  Loader2,
  Monitor,
  Moon,
  Plus,
  Sparkles,
  Sun,
  SunMoon,
  Upload,
} from 'lucide-react'
import { toast } from 'sonner'
import { api, describe, errorText, modelsChanged, RequestError, type ChatGPTStatus } from './api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import {
  SettingsGroup,
  SettingsNav,
  SettingsNavItem,
  SettingsPane,
  SettingsPanel,
  SettingsRow,
  SettingsSegmented,
  SettingsSwitch,
} from '@/components/ui/chatcn/settings-panel'
import { getPrefs, openSettings, setPref, usePrefs, useSettingsSection, type Prefs } from '@/lib/prefs'
import { t, tn } from '@/lib/i18n'
import { shortcutGroups } from '@/lib/shortcuts'
import { setTheme, useTheme, type Theme } from '@/lib/theme'
import PlanBadge from './PlanBadge'
import { useMe } from '@/components/AccountMenu'

type Props = { status: ChatGPTStatus | null; onChange: (s: ChatGPTStatus) => void }

// Settings as a modal over whatever page is open (Prism-style): sections on the left, rows on the right.
export default function SettingsDialog({ status, onChange }: Props) {
  const section = useSettingsSection()
  return (
    <Dialog open={section !== null} onOpenChange={(o) => !o && openSettings(null)}>
      <DialogContent className="h-[min(640px,calc(100dvh-2rem))] gap-0 overflow-hidden rounded-2xl p-0 sm:max-w-[860px]">
        <DialogTitle className="sr-only">{t('Settings')}</DialogTitle>
        <DialogDescription className="sr-only">{t('Appearance, editor and ChatGPT settings')}</DialogDescription>
        <SettingsPanel value={section ?? 'general'} onValueChange={(v) => openSettings(v)} className="h-full">
          <SettingsNav title={t('Settings')} className="bg-foreground/[0.025]">
            <SettingsNavItem value="general" icon={<SunMoon />}>
              {t('General')}
            </SettingsNavItem>
            <SettingsNavItem value="editor" icon={<Code2 />}>
              {t('Editor')}
            </SettingsNavItem>
            <SettingsNavItem value="assistant" icon={<Sparkles />} meta={status?.ai_ready ? undefined : t('Off')}>
              {t('Assistant')}
            </SettingsNavItem>
            <SettingsNavItem value="integrations" icon={<GitBranch />}>
              {t('Integrations')}
            </SettingsNavItem>
            <SettingsNavItem value="shortcuts" icon={<Keyboard />}>
              {t('Shortcuts')}
            </SettingsNavItem>
          </SettingsNav>
          <General />
          <EditorPane />
          <AssistantPane status={status} onChange={onChange} />
          <Integrations />
          <SettingsPane
            value="shortcuts"
            title={t('Keyboard shortcuts')}
            description={t('Hold ⌘ (Ctrl) anywhere, or press ?, to see them over the page.')}
          >
            {shortcutGroups.map((g) => (
              <SettingsGroup key={g.title} title={g.title}>
                {g.items.map(([k, v]) => (
                  <SettingsRow key={k + v} label={v}>
                    <kbd className="rounded border bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
                      {k}
                    </kbd>
                  </SettingsRow>
                ))}
              </SettingsGroup>
            ))}
          </SettingsPane>
        </SettingsPanel>
      </DialogContent>
    </Dialog>
  )
}

function General() {
  const { theme } = useTheme()
  return (
    <SettingsPane value="general" title={t('General')}>
      <SettingsGroup>
        <SettingsRow label={t('Interface theme')} description={t('System follows your operating system.')}>
          <SettingsSegmented
            value={theme}
            onValueChange={(v) => setTheme(v as Theme)}
            options={[
              { value: 'light', label: t('Light'), icon: <Sun /> },
              { value: 'dark', label: t('Dark'), icon: <Moon /> },
              { value: 'system', label: t('System'), icon: <Monitor /> },
            ]}
          />
        </SettingsRow>
        <SettingsRow
          label={t('Language')}
          description={t('System follows your browser. Changing it reloads the page.')}
        >
          <SettingsSegmented
            value={getPrefs().language}
            onValueChange={(v) => {
              setPref('language', v as Prefs['language'])
              location.reload()
            }}
            options={[
              { value: 'system', label: t('System') },
              { value: 'en', label: 'English' }, // language names stay in their own language
              { value: 'es', label: 'Español' },
            ]}
          />
        </SettingsRow>
      </SettingsGroup>
      <Account />
    </SettingsPane>
  )
}

function EditorPane() {
  const prefs = usePrefs()
  return (
    <SettingsPane value="editor" title={t('Editor')} description={t('Stored in this browser.')}>
      <SettingsGroup>
        <SettingsRow label={t('Font size')} description={t('Text size in the editor.')}>
          <SettingsSegmented
            value={prefs.fontSize}
            onValueChange={(v) => setPref('fontSize', v as Prefs['fontSize'])}
            options={[
              { value: 'small', label: t('Small') },
              { value: 'normal', label: t('Normal') },
              { value: 'large', label: t('Large') },
            ]}
          />
        </SettingsRow>
        <SettingsRow label={t('Word wrap')} description={t('Wrap long lines instead of scrolling sideways.')}>
          <SettingsSwitch checked={prefs.wrap} onCheckedChange={(v) => setPref('wrap', v)} />
        </SettingsRow>
        <SettingsRow label={t('Word count')}>
          <SettingsSwitch checked={prefs.wordCount} onCheckedChange={(v) => setPref('wordCount', v)} />
        </SettingsRow>
        <SettingsRow
          label={<span className="font-normal">{t('Include titles')}</span>}
          disabled={!prefs.wordCount}
          className="pl-5"
        >
          <SettingsSwitch checked={prefs.wordCountTitles} onCheckedChange={(v) => setPref('wordCountTitles', v)} />
        </SettingsRow>
      </SettingsGroup>
    </SettingsPane>
  )
}

function AssistantPane({ status, onChange }: Props) {
  const prefs = usePrefs()
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  async function run(fn: () => Promise<ChatGPTStatus>, ok: string) {
    setBusy(true)
    try {
      onChange(await fn())
      toast.success(ok)
    } catch (e) {
      toast.error(e instanceof RequestError ? describe(e.error) : String(e))
    } finally {
      setBusy(false)
    }
  }

  const s = status?.status
  return (
    <SettingsPane
      value="assistant"
      title={t('Assistant')}
      description={t('Refract uses your ChatGPT plan or your own API keys for AI requests.')}
    >
      <SettingsGroup title={t('ChatGPT account')}>
        {!status ? (
          <SettingsRow label={t('Loading…')} />
        ) : s?.signed_in ? (
          <>
            <SettingsRow
              label={
                <span className="flex items-center gap-2">
                  <CheckCircle2 className="size-4 text-success" /> {s.email}
                </span>
              }
              description={
                s.limit_reached
                  ? describe({ kind: 'usage_limit', message: '' })
                  : s.plan_granted
                    ? t('Signed in. Requests count against your ChatGPT plan.')
                    : describe({ kind: 'plan_not_granted', message: '' })
              }
            >
              {s.plan_granted && <PlanBadge manageUsage={status.manage_usage} />}
            </SettingsRow>
            <SettingsRow
              label={t('Credentials')}
              description={t('Replace them after running {cmd} again.', { cmd: 'refract login' })}
            >
              <Button size="sm" variant="secondary" disabled={busy} onClick={() => input.current?.click()}>
                <Upload /> {t('Replace…')}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => run(() => api('/api/chatgpt/signout', { method: 'POST' }), t('Signed out'))}
              >
                {t('Sign out')}
              </Button>
            </SettingsRow>
            <SettingsRow label={t('Usage')} description={t('Limits and remaining usage are managed by ChatGPT.')}>
              <Button
                size="sm"
                variant="ghost"
                render={<a href={status.manage_usage} target="_blank" rel="noreferrer" />}
                nativeButton={false}
              >
                {t('Manage usage')} <ExternalLink />
              </Button>
            </SettingsRow>
          </>
        ) : (
          <SettingsRow
            stacked
            label={t('Not connected')}
            description={
              <>
                {t('Sign-in happens on your own computer: run')}{' '}
                <code className="rounded bg-muted px-1 py-0.5 text-foreground">refract login</code>, {t('choose')}{' '}
                <em>Continue with ChatGPT</em>, {t('then upload the credentials file it writes.')}
              </>
            }
          >
            <Button size="sm" disabled={busy} onClick={() => input.current?.click()}>
              <Upload /> {t('Upload credentials…')}
            </Button>
          </SettingsRow>
        )}
      </SettingsGroup>
      <Providers onChange={() => api<ChatGPTStatus>('/api/chatgpt/status').then(onChange, () => {})} />
      <SettingsGroup title={t('Chat')}>
        <SettingsRow
          label={t('When collapsed')}
          description={t('Keep an input bar under the editor, or tuck the chat into a bubble.')}
        >
          <SettingsSegmented
            value={prefs.chatBubble ? 'bubble' : 'bar'}
            onValueChange={(v) => setPref('chatBubble', v === 'bubble')}
            options={[
              { value: 'bar', label: t('Input bar') },
              { value: 'bubble', label: t('Bubble') },
            ]}
          />
        </SettingsRow>
      </SettingsGroup>
      <input
        ref={input}
        type="file"
        accept=".json,application/json"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f)
            run(
              async () => api<ChatGPTStatus>('/api/chatgpt/credentials', { method: 'POST', body: await f.text() }),
              t('Credentials imported'),
            )
          e.target.value = ''
        }}
      />
    </SettingsPane>
  )
}

// Password change, for accounts Refract signs in itself (REFRACT_AUTH=password).
function Account() {
  const me = useMe()
  const [has, setHas] = useState(false)
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (me?.can_sign_out)
      api<{ has_password: boolean }>('/api/account').then(
        (a) => setHas(a.has_password),
        () => {},
      )
  }, [me?.can_sign_out])
  if (!me?.can_sign_out || !has) return null

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api('/api/account/password', { method: 'POST', body: { current, new: next } })
      setCurrent('')
      setNext('')
      toast.success(t('Password changed. Other devices were signed out.'))
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <SettingsGroup title={t('Account')}>
      <SettingsRow stacked label={t('Password')} description={t('Signed in as {email}.', { email: me.email })}>
        <form onSubmit={save} className="grid w-full gap-2 sm:grid-cols-[1fr_1fr_auto]">
          <Input
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            placeholder={t('Current password')}
            aria-label={t('Current password')}
          />
          <Input
            type="password"
            autoComplete="new-password"
            required
            minLength={8}
            value={next}
            onChange={(e) => setNext(e.target.value)}
            placeholder={t('New password (8+ characters)')}
            aria-label={t('New password')}
          />
          <Button type="submit" size="sm" className="h-9" disabled={busy}>
            {busy && <Loader2 className="animate-spin" />} {t('Change')}
          </Button>
        </form>
      </SettingsRow>
    </SettingsGroup>
  )
}

type Kind = {
  id: string
  name: string
  needs_key: boolean
  base_url: string
  needs_base: boolean
  key_help_url?: string
}
type ProviderConfig = { id: string; kind: string; name: string; base_url?: string; models?: string[]; has_key: boolean }
type ProvidersState = { kinds: Kind[]; configured: ProviderConfig[] }
type Draft = {
  id: string
  kind: string
  name: string
  api_key: string
  base_url: string
  models: string
  editing: boolean
}

const blank = (kind: Kind, taken: string[]): Draft => {
  let id = kind.id
  for (let n = 2; taken.includes(id); n++) id = `${kind.id}-${n}`
  return { id, kind: kind.id, name: '', api_key: '', base_url: '', models: '', editing: false }
}

// API-key providers (OpenAI, Anthropic, Gemini, OpenRouter, Ollama…). Keys go
// to the server, sealed; the browser only learns that one is saved.
function Providers({ onChange }: { onChange: () => void }) {
  const [state, setState] = useState<ProvidersState | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState('')

  useEffect(() => {
    api<ProvidersState>('/api/providers').then(setState, (e) => toast.error(errorText(e)))
  }, [])

  const changed = (s: ProvidersState) => {
    setState(s)
    onChange()
    modelsChanged()
  }
  async function save(d: Draft) {
    setBusy('save')
    try {
      const body = {
        kind: d.kind,
        name: d.name.trim(),
        api_key: d.api_key.trim(),
        base_url: d.base_url.trim(),
        models: d.models.split(/[\s,]+/).filter(Boolean),
      }
      changed(await api<ProvidersState>(`/api/providers/${encodeURIComponent(d.id)}`, { method: 'PUT', body }))
      setDraft(null)
      test(d.id)
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy('')
    }
  }
  async function test(id: string) {
    setBusy(id)
    try {
      const r = await api<{ models: number }>(`/api/providers/${encodeURIComponent(id)}/test`, { method: 'POST' })
      toast.success(tn(r.models, 'Connected: {n} model available', 'Connected: {n} models available'))
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy('')
    }
  }
  async function remove(id: string) {
    setBusy(id)
    try {
      changed(await api<ProvidersState>(`/api/providers/${encodeURIComponent(id)}`, { method: 'DELETE' }))
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy('')
    }
  }

  if (!state) return null
  const kindOf = (id: string) => state.kinds.find((k) => k.id === id)
  const kind = draft && kindOf(draft.kind)
  return (
    <SettingsGroup
      title={t('API providers')}
      description={t('Use your own API keys or a local model server. Their models appear in the model picker.')}
    >
      {state.configured.map((c) => (
        <SettingsRow
          key={c.id}
          label={c.name || kindOf(c.kind)?.name || c.kind}
          description={[kindOf(c.kind)?.name, c.base_url, c.has_key ? t('key saved') : ''].filter(Boolean).join(' · ')}
        >
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => test(c.id)}>
            {busy === c.id ? <Loader2 className="animate-spin" /> : null} {t('Test')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={!!busy}
            onClick={() =>
              setDraft({
                id: c.id,
                kind: c.kind,
                name: c.name,
                api_key: '',
                base_url: c.base_url ?? '',
                models: (c.models ?? []).join(', '),
                editing: true,
              })
            }
          >
            {t('Edit')}
          </Button>
          <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => remove(c.id)}>
            {t('Remove')}
          </Button>
        </SettingsRow>
      ))}
      {draft && kind ? (
        <SettingsRow
          stacked
          label={draft.editing ? t('Edit {name}', { name: draft.name || kind.name }) : t('Add a provider')}
        >
          <form
            className="grid w-full gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              save(draft)
            }}
          >
            {!draft.editing && (
              <Select
                value={draft.kind}
                items={state.kinds.map((k) => ({ value: k.id, label: k.name }))}
                onValueChange={(v) => {
                  const k = v && kindOf(v)
                  if (k)
                    setDraft(
                      blank(
                        k,
                        state.configured.map((c) => c.id),
                      ),
                    )
                }}
              >
                <SelectTrigger size="sm" className="w-full" aria-label={t('Provider')}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {state.kinds.map((k) => (
                    <SelectItem key={k.id} value={k.id}>
                      {k.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <Input
              value={draft.name}
              onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              placeholder={t('Name (optional)')}
              aria-label={t('Name')}
            />
            {(kind.needs_key || kind.id === 'compat') && (
              <Input
                type="password"
                autoComplete="off"
                value={draft.api_key}
                onChange={(e) => setDraft({ ...draft, api_key: e.target.value })}
                placeholder={
                  draft.editing
                    ? t('API key (leave empty to keep the saved one)')
                    : kind.needs_key
                      ? t('API key')
                      : t('API key (if the server needs one)')
                }
                aria-label={t('API key')}
                required={kind.needs_key && !draft.editing}
              />
            )}
            <Input
              value={draft.base_url}
              onChange={(e) => setDraft({ ...draft, base_url: e.target.value })}
              placeholder={kind.base_url || 'https://…/v1'}
              aria-label={t('Base URL')}
              required={kind.needs_base}
            />
            <Input
              value={draft.models}
              onChange={(e) => setDraft({ ...draft, models: e.target.value })}
              placeholder={t('Extra model ids, comma-separated (optional)')}
              aria-label={t('Extra model ids')}
            />
            <div className="flex items-center gap-2">
              {kind.key_help_url && (
                <a
                  className="text-xs text-muted-foreground underline"
                  href={kind.key_help_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t('Get an API key')}
                </a>
              )}
              <span className="flex-1" />
              <Button type="button" size="sm" variant="ghost" onClick={() => setDraft(null)}>
                {t('Cancel')}
              </Button>
              <Button type="submit" size="sm" disabled={!!busy}>
                {busy === 'save' && <Loader2 className="animate-spin" />} {t('Save')}
              </Button>
            </div>
          </form>
        </SettingsRow>
      ) : (
        <SettingsRow label={state.configured.length ? t('Another provider') : t('No API providers yet')}>
          <Button
            size="sm"
            variant="secondary"
            onClick={() =>
              setDraft(
                blank(
                  state.kinds[0],
                  state.configured.map((c) => c.id),
                ),
              )
            }
          >
            <Plus /> {t('Add provider')}
          </Button>
        </SettingsRow>
      )}
    </SettingsGroup>
  )
}

type GitHubState = { configured: boolean; connected: boolean; login?: string }
type Device = { user_code: string; verification_uri: string; interval: number; expires_in: number }

// GitHub account, connected with the device flow: Refract shows a code, you
// enter it on github.com, and the token stays sealed on the server.
function Integrations() {
  const [gh, setGh] = useState<GitHubState | null>(null)
  const [device, setDevice] = useState<Device | null>(null)
  const [busy, setBusy] = useState(false)

  const load = () => api<GitHubState>('/api/github').then(setGh, (e) => toast.error(errorText(e)))
  useEffect(() => {
    load()
  }, [])

  // Poll while a code is shown, at GitHub's interval.
  useEffect(() => {
    if (!device) return
    let stop = false
    const tick = async () => {
      if (stop) return
      try {
        const r = await api<{ status: string; login?: string; error?: string }>('/api/github/device/poll', {
          method: 'POST',
        })
        if (r.status === 'connected') {
          setDevice(null)
          toast.success(t('Connected to GitHub as @{login}', { login: r.login ?? '' }))
          load()
          return
        }
        if (r.status !== 'pending') {
          setDevice(null)
          toast.error(r.error ?? t('The code expired: start again'))
          return
        }
      } catch (e) {
        toast.error(errorText(e))
      }
      setTimeout(tick, Math.max(device.interval, 5) * 1000)
    }
    const first = setTimeout(tick, Math.max(device.interval, 5) * 1000)
    return () => {
      stop = true
      clearTimeout(first)
    }
  }, [device])

  async function connect() {
    setBusy(true)
    try {
      const d = await api<Device>('/api/github/device', { method: 'POST' })
      setDevice(d)
      window.open(d.verification_uri, '_blank', 'noopener')
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy(false)
    }
  }

  async function disconnect() {
    await api('/api/github', { method: 'DELETE' }).catch((e) => toast.error(errorText(e)))
    load()
  }

  return (
    <SettingsPane
      value="integrations"
      title={t('Integrations')}
      description={t('Accounts Refract can use for your projects.')}
    >
      <SettingsGroup title="GitHub">
        {!gh ? (
          <SettingsRow label={t('Loading…')} />
        ) : !gh.configured ? (
          <SettingsRow
            stacked
            label={t('Not set up on this server')}
            description={t(
              'Create a GitHub OAuth App with device flow enabled and set its client ID in REFRACT_GITHUB_CLIENT_ID. You can still use any remote with a token in each project.',
            )}
          />
        ) : gh.connected ? (
          <SettingsRow
            label={
              <span className="flex items-center gap-2">
                <CheckCircle2 className="size-4 text-success" /> @{gh.login}
              </span>
            }
            description={t(
              'Projects can push to and pull from your repositories. To revoke access completely, also remove Refract in GitHub → Settings → Applications.',
            )}
          >
            <Button size="sm" variant="ghost" onClick={disconnect}>
              {t('Disconnect')}
            </Button>
          </SettingsRow>
        ) : device ? (
          <SettingsRow
            stacked
            label={t('Enter this code on GitHub')}
            description={t('Waiting for you to approve Refract on github.com…')}
          >
            <span className="flex items-center gap-2">
              <code className="rounded-md border bg-muted px-3 py-1.5 font-mono text-lg tracking-widest">
                {device.user_code}
              </code>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={t('Copy code')}
                onClick={() => navigator.clipboard.writeText(device.user_code)}
              >
                <Copy />
              </Button>
              <Button
                size="sm"
                variant="secondary"
                render={<a href={device.verification_uri} target="_blank" rel="noreferrer" />}
                nativeButton={false}
              >
                github.com/login/device <ExternalLink />
              </Button>
              <Loader2 className="size-4 animate-spin text-muted-foreground" />
            </span>
          </SettingsRow>
        ) : (
          <SettingsRow
            label={t('Not connected')}
            description={t('Connect to push projects to your repositories, or create new ones.')}
          >
            <Button size="sm" disabled={busy} onClick={connect}>
              <GitBranch /> {t('Connect GitHub')}
            </Button>
          </SettingsRow>
        )}
      </SettingsGroup>
    </SettingsPane>
  )
}
