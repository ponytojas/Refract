import { useEffect, useState } from 'react'
import { ExternalLink, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, errorText, RequestError, type ChatGPTStatus } from './api'
import { Button } from '@/components/ui/button'
import { SettingsRow } from '@/components/ui/chatcn/settings-panel'
import { failureText, settled, type LoginState } from '@/lib/chatgpt-login'
import { t } from '@/lib/i18n'

type Props = {
  // again: the account is connected and this signs in anew (to renew or switch).
  again?: boolean
  onChange: (s: ChatGPTStatus) => void
}

// Sign in with ChatGPT inside Refract, for a server on this computer
// (status.sign_in_here): the server waits for OpenAI's redirect to
// 127.0.0.1 while the person finishes in their browser.
export default function ChatGPTSignIn({ again, onChange }: Props) {
  const [login, setLogin] = useState<LoginState | null>(null)
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)

  // A sign-in started before Settings was closed is still running on the
  // server, or finished meanwhile: then the connection shown is stale.
  useEffect(() => {
    let stop = false
    api<LoginState>('/api/chatgpt/login')
      .then(async (st) => {
        if (stop) return
        if (st.state === 'pending') setLogin(st)
        else if (st.state === 'done') {
          const status = await api<ChatGPTStatus>('/api/chatgpt/status')
          if (!stop) onChange(status)
        }
      })
      .catch(() => {})
    return () => {
      stop = true
    }
  }, [onChange])

  const pending = login?.state === 'pending'
  useEffect(() => {
    if (!pending) return
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    async function tick() {
      try {
        const st = await api<LoginState>('/api/chatgpt/login')
        if (stop) return
        if (settled(st)) {
          setLogin(st)
          if (st.state === 'done') {
            onChange(await api<ChatGPTStatus>('/api/chatgpt/status'))
            toast.success(t('Signed in with ChatGPT'))
            setLogin(null)
          }
          return
        }
      } catch {
        // A missed poll is retried; the server keeps waiting.
      }
      if (!stop) timer = setTimeout(tick, 1500)
    }
    timer = setTimeout(tick, 1500)
    return () => {
      stop = true
      clearTimeout(timer)
    }
  }, [pending, onChange])

  async function start(fresh = false) {
    setBusy(true)
    try {
      const r = await api<{ url: string; opened: boolean }>('/api/chatgpt/login', {
        method: 'POST',
        body: { new: fresh },
      })
      setUrl(r.url)
      setLogin({ state: 'pending' })
      // The server opens the browser itself (it runs on this computer). If it
      // couldn't, try from the page; a blocked window leaves the link below.
      if (!r.opened) window.open(r.url, '_blank', 'noopener')
    } catch (e) {
      // 503: the sign-in failed before it could start (the callback port is taken).
      if (e instanceof RequestError && e.status === 503)
        setLogin({ state: 'failed', kind: e.error.kind as LoginState['kind'], message: e.error.message })
      else toast.error(errorText(e))
    } finally {
      setBusy(false)
    }
  }

  async function cancel() {
    setBusy(true)
    await api('/api/chatgpt/login', { method: 'DELETE' }).catch(() => {})
    setLogin(null)
    setBusy(false)
  }

  if (pending) {
    return (
      <SettingsRow
        stacked
        label={
          <span className="flex items-center gap-2">
            <Loader2 className="size-4 animate-spin text-muted-foreground motion-reduce:animate-none" />
            {t('Waiting for ChatGPT…')}
          </span>
        }
        description={
          <>
            {t('Finish signing in in your browser, then come back here.')}{' '}
            {url && (
              <a href={url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 underline">
                {t('Open the sign-in page')} <ExternalLink className="size-3" />
              </a>
            )}
          </>
        }
      >
        <Button size="sm" variant="ghost" disabled={busy} onClick={cancel}>
          {t('Cancel')}
        </Button>
      </SettingsRow>
    )
  }

  const failed = login?.state === 'failed' ? login : null
  return (
    <SettingsRow
      stacked={!again}
      label={again ? t('Sign in again') : t('Not connected')}
      description={
        failed ? (
          <span className="text-destructive">{failureText(failed, t)}</span>
        ) : again ? (
          t('To renew access or use another ChatGPT account.')
        ) : (
          t('Use your ChatGPT plan for the assistant. Your browser opens to finish signing in.')
        )
      }
    >
      {failed?.kind === 'other_account' ? (
        <Button size="sm" disabled={busy} onClick={() => start(true)}>
          {t('Use the new account')}
        </Button>
      ) : (
        <Button size="sm" variant={again ? 'secondary' : 'default'} disabled={busy} onClick={() => start()}>
          {failed ? t('Try again') : again ? t('Sign in again') : t('Sign in with ChatGPT')}
        </Button>
      )}
    </SettingsRow>
  )
}
