import { lang, t } from '@/lib/i18n'
import { chatgptOffered } from '@/lib/mode'

// Every /api call carries X-Refract (the server rejects requests without it: CSRF),
// and the interface language, which the server uses for the emails it sends.
const headers = { 'X-Refract': '1', 'X-Refract-Lang': lang }

export type ApiError = { kind: string; message: string; code?: string; param?: string }

export class RequestError extends Error {
  constructor(
    public error: ApiError,
    public status = 0, // the HTTP status, when the request reached the server
  ) {
    super(error.message)
  }
}

/** Fired when an API call learns that a project is in the trash (the server says so to its owner); detail.project is its id. */
export const TRASHED_EVENT = 'refract:trashed'

/** Whether an error means "this project is in the trash". */
export const isTrashed = (e: unknown): boolean => e instanceof RequestError && e.error.kind === 'project_trashed'

function signalTrashed(path: string) {
  const project = /^\/api\/projects\/([^/?]+)/.exec(path)?.[1]
  window.dispatchEvent(new CustomEvent(TRASHED_EVENT, { detail: { project } }))
}

// The one place every failed API call becomes an error, so none misses the trashed signal.
function failure(res: Response, data: { error?: ApiError } | null, path: string): RequestError {
  const e = new RequestError(data?.error ?? { kind: 'other', message: `HTTP ${res.status}` }, res.status)
  if (isTrashed(e)) signalTrashed(path)
  return e
}

// body: an object is sent as JSON; a string, Blob or FormData is sent as is.
export async function api<T>(
  path: string,
  init: { method?: string; body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const raw = typeof init.body === 'string' || init.body instanceof Blob || init.body instanceof FormData
  const res = await fetch(path, {
    method: init.method ?? 'GET',
    headers: init.body === undefined || raw ? headers : { ...headers, 'Content-Type': 'application/json' },
    body: init.body === undefined ? undefined : raw ? (init.body as BodyInit) : JSON.stringify(init.body),
    signal: init.signal,
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) throw failure(res, data, path)
  return data as T
}

// Reads a server-sent event stream from a POST (EventSource can't send headers or a body).
export async function stream(path: string, body: unknown, onEvent: (ev: any) => void, signal?: AbortSignal) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { ...headers, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok || !res.body) {
    const data = await res.json().catch(() => null)
    onEvent({ type: 'error', error: failure(res, data, path).error })
    return
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) return
    buf += value
    let i
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const chunk = buf.slice(0, i)
      buf = buf.slice(i + 2)
      if (chunk.startsWith('data: ')) {
        const ev = JSON.parse(chunk.slice(6))
        // The trash can also land after the stream has started.
        if (ev.type === 'error' && ev.error?.kind === 'project_trashed') signalTrashed(path)
        onEvent(ev)
      }
    }
  }
}

export type Effort = { effort: string; description: string }
export type Model = {
  slug: string
  display_name: string
  description: string
  default_effort: string
  efforts: Effort[]
  provider?: string // "chatgpt" or a configured provider's id
  provider_name?: string
}

/** One model's token usage over a period (GET /api/usage). Cached and reasoning tokens are parts of input and output. */
export type ModelUsage = {
  model: string
  calls: number
  input_tokens: number
  cached_tokens: number
  output_tokens: number
  reasoning_tokens: number
  included?: boolean // made with the included AI (S1), not the account's own keys
}

/** This month's included AI (S1): its limit and what is spent, in USD; resets_at in Unix seconds. */
export type Allowance = { limit_usd: number; spent_usd: number; resets_at: number }

/** GET /api/usage. allowance is set only for accounts with the included AI. */
export type UsageReport = { models: ModelUsage[]; allowance?: Allowance }

export type ChatGPTStatus = {
  status: { provider: string; signed_in: boolean; plan_granted: boolean; email?: string; limit_reached: boolean }
  ai_ready: boolean // ChatGPT plan or at least one API provider
  welcome_seen: boolean
  manage_usage: string
  default_model: string
}

// User-facing text per error kind.
export function describe(e: ApiError): string {
  switch (e.kind) {
    case 'not_signed_in':
      return chatgptOffered()
        ? t(
            'No AI connected. Sign in with ChatGPT (run `refract login` and upload the credentials) or add an API provider in Settings → Assistant.',
          )
        : t('No AI connected. Add an API provider in Settings → Assistant.')
    case 'plan_not_granted':
      return t('ChatGPT plan usage was not granted. Run `refract login` again and allow plan usage.')
    case 'usage_limit':
      return t(
        'You have reached a usage limit on your ChatGPT plan or for this app. Review it in ChatGPT settings → Usage.',
      )
    case 'not_eligible':
      return t('ChatGPT plan usage is not available for this account: {message}', { message: e.message })
    case 'model_unavailable':
      return t('This model is not available. Pick another one.')
    case 'project_trashed':
      return t('This project is in the trash. Restore it to keep working.')
    case 'project_busy':
      return t('The assistant is working on this project. Stop it or wait until it finishes, then try again.')
    case 'bad_login':
      return t('Wrong email or password.')
    case 'wrong_password':
      return t('The current password is wrong.')
    case 'too_many_attempts':
      return t('Too many failed attempts. Try again in a few minutes.')
    case 'email_unverified':
      return t(
        'Confirm your email address to create or import projects. Open the link we sent you, or send a new one from Settings → Account.',
      )
    case 'too_many_emails':
      return t('Too many emails to this address. Try again in a few minutes.')
    case 'mail_unavailable':
      return t("This server can't send emails, so this isn't available. Ask the person who runs it.")
    case 'signup_closed':
      return t("This server doesn't take new accounts. Ask the person who runs it to add you.")
    case 'invalid_token':
      return t('This link has expired or was already used.')
    case 'password_required':
      return t('Enter your password to continue.')
    case 'reauth_required':
      return t('For your security, sign in again, then delete your account within 10 minutes.')
    case 'login_required':
      return t('Your session ended. Reload the page to sign in again.')
    case 'invalid_key':
      return t('The provider rejected the API key: {message}', { message: e.message })
    case 'quota':
      return t('The provider has no credit left on this API key: {message}', { message: e.message })
    case 'unsupported':
      return t('Request not supported on plan usage ({param}): {message}', {
        param: e.param ?? t('unknown'),
        message: e.message,
      })
    case 'unavailable':
      return t('The AI provider is temporarily unavailable. Try again shortly. ({message})', { message: e.message })
    default:
      return e.message
  }
}

// Downloads a file from an API route (a plain link can't send X-Refract).
export async function download(path: string, filename: string) {
  const res = await fetch(path, { headers })
  if (!res.ok)
    throw new RequestError({ kind: 'other', message: t('Download failed: HTTP {status}', { status: res.status }) })
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export type Project = { id: string; name: string; main_file: string; created_at: string; updated_at: string }
export type TrashedProject = Project & { deleted_at: string; purge_at: string }
export type Trash = { days: number; projects: TrashedProject[] }
export type Warning = { file: string; line: number; kind: string; message: string }
export type ImportResult = { project: Project; warnings: Warning[]; skipped?: string[]; candidates?: string[] }

export function errorText(e: unknown): string {
  return e instanceof RequestError ? describe(e.error) : String(e)
}

export type FileEntry = { path: string; dir: boolean; size: number; mtime: number }
export type OpenedFile = { text: true; content: string } | { text: false; blob: Blob; type: string; size: number }

export const fileURL = (id: string, path: string) => `/api/projects/${id}/file?path=${encodeURIComponent(path)}`

export async function readFile(id: string, path: string): Promise<OpenedFile> {
  const res = await fetch(fileURL(id, path), { headers })
  if (!res.ok) {
    throw failure(res, await res.json().catch(() => null), fileURL(id, path))
  }
  if (res.headers.get('X-Refract-Text') === '1') return { text: true, content: await res.text() }
  const blob = await res.blob()
  return { text: false, blob, type: res.headers.get('Content-Type') ?? '', size: blob.size }
}

export async function writeFile(id: string, path: string, body: string | Blob) {
  const size = typeof body === 'string' ? body.length : body.size
  const res = await fetch(fileURL(id, path), {
    method: 'PUT',
    headers,
    body,
    keepalive: size < 60_000, // lets a save started while the page unloads still finish
  })
  if (!res.ok) {
    throw failure(res, await res.json().catch(() => null), fileURL(id, path))
  }
}

export const removeFile = (id: string, path: string) => api(fileURL(id, path), { method: 'DELETE' })
export const mkdir = (id: string, path: string) =>
  api(`/api/projects/${id}/mkdir?path=${encodeURIComponent(path)}`, { method: 'POST' })
export const move = (id: string, from: string, to: string) =>
  api(`/api/projects/${id}/move`, { method: 'POST', body: { from, to } })

// Fired after the user changes their API providers, so open model pickers reload.
export const modelsChanged = () => window.dispatchEvent(new Event('refract:models'))
