import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { api, errorText, RequestError } from './api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { lang, languageNames, t } from '@/lib/i18n'
import { linkToken } from '@/lib/account'
import { setMode } from '@/lib/mode'
import { Link, reloadTo, usePath } from '@/lib/router'

// Why an OIDC sign-in came back refused (?login_error=…).
const oidcErrors: Record<string, string> = {
  not_allowed: t('There is no Refract account for that email. Ask whoever runs this server to add you.'),
  expired: t('The sign-in took too long or started in another browser. Try again.'),
  failed: t('Signing in with the provider failed. Try again, or use your password.'),
  unverified_email: t(
    "Your provider didn't confirm this email address, and this account has a password: sign in with it instead.",
  ),
}

type Config = { oidc: string; mode: string; signup: boolean; reset: boolean }

// Shown instead of the app when the server asks for a session (REFRACT_AUTH=builtin):
// sign-in, and the account screens people reach before they have a session.
// signedIn: this browser already has one (the emailed links open here either way).
export default function Login({ signedIn = false }: { signedIn?: boolean }) {
  const path = usePath()
  const [config, setConfig] = useState<Config>({ oidc: '', mode: '', signup: false, reset: false })

  useEffect(() => {
    api<Config>('/api/auth/config').then(
      (c) => {
        setConfig(c)
        setMode(c.mode)
      },
      () => {},
    )
  }, [])

  switch (path) {
    case '/signup':
      return <SignUp />
    case '/forgot':
      return <Forgot />
    case '/verify':
      return <Verify signedIn={signedIn} />
    case '/reset':
      return <ResetPassword />
    default:
      return <SignIn config={config} />
  }
}

function Screen({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <main className="flex min-h-dvh items-center justify-center bg-background p-4">
      <div className="w-full max-w-sm space-y-6">
        <div className="flex flex-col items-center gap-3 text-center">
          <img src="/logo.svg" alt="" className="size-10" />
          <h1 className="text-xl font-semibold">{title}</h1>
        </div>
        {children}
      </div>
    </main>
  )
}

const cardClass = 'space-y-3 rounded-2xl border bg-card p-5 shadow-xs'
const linkClass = 'text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline'

function ErrorLine({ error }: { error: string }) {
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {error}
    </p>
  ) : null
}

function Submit({ busy, children }: { busy: boolean; children: React.ReactNode }) {
  return (
    <Button type="submit" className="w-full" disabled={busy}>
      {busy && <Loader2 className="animate-spin" />} {children}
    </Button>
  )
}

// Runs an API call for a form: busy and error state, errors as readable text.
function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await fn()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }
  return { busy, error, setError, run }
}

const accountPages = ['/signup', '/forgot', '/verify', '/reset']

// Signed in on one of the account pages: go to the app. Elsewhere the page reloads in place.
function enterApp() {
  reloadTo(accountPages.includes(location.pathname) ? '/' : undefined)
}

function SignIn({ config }: { config: Config }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const { busy, error, setError, run } = useAction()
  const [deleted] = useState(() => new URLSearchParams(location.search).get('account_deleted') === '1')

  useEffect(() => {
    const code = new URLSearchParams(location.search).get('login_error')
    if (code) setError(oidcErrors[code] ?? oidcErrors.failed)
    if (location.search.includes('login_error') || location.search.includes('account_deleted'))
      history.replaceState(null, '', location.pathname)
  }, [setError])

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    run(async () => {
      await api('/api/auth/login', { method: 'POST', body: { email, password } })
      enterApp()
    })
  }

  return (
    <Screen title={t('Sign in to Refract')}>
      <form onSubmit={submit} className={cardClass}>
        {deleted && (
          <p role="status" className="text-sm text-muted-foreground">
            {t('Your account and everything in it were deleted.')}
          </p>
        )}
        <Input
          type="email"
          autoComplete="username"
          autoFocus
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('Email')}
          aria-label={t('Email')}
        />
        <Input
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t('Password')}
          aria-label={t('Password')}
        />
        {config.reset && (
          <Link to="/forgot" className={linkClass}>
            {t('Forgot your password?')}
          </Link>
        )}
        <ErrorLine error={error} />
        <Submit busy={busy}>{t('Sign in')}</Submit>
        {config.oidc && (
          <>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="h-px flex-1 bg-border" /> {t('or')} <span className="h-px flex-1 bg-border" />
            </div>
            <Button
              type="button"
              variant="secondary"
              className="w-full"
              nativeButton={false}
              render={<a href="/api/auth/oidc/start" />}
            >
              {t('Continue with {name}', { name: config.oidc })}
            </Button>
          </>
        )}
      </form>
      {config.signup && (
        <p className="text-center text-sm text-muted-foreground">
          {t('New to Refract?')}{' '}
          <Link to="/signup" className="text-foreground underline-offset-4 hover:underline">
            {t('Create an account')}
          </Link>
        </p>
      )}
    </Screen>
  )
}

// What an account form says once the server has taken it: look at your email.
function CheckEmail({ message }: { message: string }) {
  return (
    <Screen title={t('Check your email')}>
      <div className={cardClass} aria-live="polite">
        <p className="text-sm">{message}</p>
        <p className="text-sm text-muted-foreground">
          {t("If it doesn't arrive in a few minutes, check your spam folder.")}
        </p>
        <Link to="/" className={linkClass}>
          {t('Back to sign in')}
        </Link>
      </div>
    </Screen>
  )
}

function SignUp() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [emailLang, setEmailLang] = useState<'en' | 'es'>(lang)
  const [sent, setSent] = useState(false)
  const { busy, error, run } = useAction()
  if (sent)
    return <CheckEmail message={t('We sent a message to {email}. Open the link in it to continue.', { email })} />

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    run(async () => {
      await api('/api/auth/signup', { method: 'POST', body: { email, password, lang: emailLang } })
      setSent(true)
    })
  }
  return (
    <Screen title={t('Create your account')}>
      <form onSubmit={submit} className={cardClass}>
        <Input
          type="email"
          autoComplete="email"
          autoFocus
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('Email')}
          aria-label={t('Email')}
        />
        <Input
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t('Password')}
          aria-label={t('Password')}
          aria-describedby="password-hint"
        />
        <p id="password-hint" className="text-xs text-muted-foreground">
          {t('8 or more characters')}
        </p>
        <div className="space-y-1.5">
          <span id="email-lang" className="text-sm text-muted-foreground">
            {t('Language for emails')}
          </span>
          <Select items={languageNames} value={emailLang} onValueChange={(v) => v && setEmailLang(v as 'en' | 'es')}>
            <SelectTrigger className="w-full" aria-labelledby="email-lang">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Object.entries(languageNames).map(([code, name]) => (
                <SelectItem key={code} value={code}>
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <ErrorLine error={error} />
        <Submit busy={busy}>{t('Create account')}</Submit>
      </form>
      <p className="text-center text-sm text-muted-foreground">
        {t('Already have an account?')}{' '}
        <Link to="/" className="text-foreground underline-offset-4 hover:underline">
          {t('Sign in')}
        </Link>
      </p>
    </Screen>
  )
}

function Forgot() {
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const { busy, error, run } = useAction()
  if (sent)
    return (
      <CheckEmail
        message={t(
          'If {email} has an account, we sent it a link to choose a new password. The link works for 1 hour.',
          {
            email,
          },
        )}
      />
    )

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    run(async () => {
      await api('/api/auth/forgot', { method: 'POST', body: { email } })
      setSent(true)
    })
  }
  return (
    <Screen title={t('Reset your password')}>
      <form onSubmit={submit} className={cardClass}>
        <Input
          type="email"
          autoComplete="email"
          autoFocus
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('Email')}
          aria-label={t('Email')}
        />
        <ErrorLine error={error} />
        <Submit busy={busy}>{t('Send reset link')}</Submit>
        <Link to="/" className={linkClass}>
          {t('Back to sign in')}
        </Link>
      </form>
    </Screen>
  )
}

// The token of an emailed link: read from the fragment once, then removed from the
// address bar. Kept at module level so a re-render (or StrictMode) doesn't lose it.
let seenToken: string | null | undefined
function useLinkToken(): string | null {
  if (seenToken === undefined) {
    seenToken = linkToken(location.hash)
    if (location.hash) history.replaceState(history.state, '', location.pathname + location.search)
  }
  return seenToken
}

function Done({ title, message }: { title: string; message: string }) {
  return (
    <Screen title={title}>
      <div className={cardClass} aria-live="polite">
        <p className="text-sm">{message}</p>
        <Button className="w-full" onClick={() => reloadTo('/')}>
          {t('Open Refract')}
        </Button>
      </div>
    </Screen>
  )
}

function Verify({ signedIn }: { signedIn: boolean }) {
  const token = useLinkToken()
  const [password, setPassword] = useState('')
  const [needPassword, setNeedPassword] = useState(!signedIn)
  const [done, setDone] = useState(false)
  const [invalid, setInvalid] = useState(token === null)
  const { busy, error, run } = useAction()
  const title = t('Confirm your email')

  if (done) return <Done title={title} message={t('Email confirmed.')} />
  if (invalid)
    return (
      <Screen title={title}>
        <div className={cardClass}>
          <p role="alert" className="text-sm text-destructive">
            {t('This link has expired or was already used.')}
          </p>
          <p className="text-sm text-muted-foreground">{t('Sign in and send a new link from Settings → Account.')}</p>
          <Link to="/" className={linkClass}>
            {t('Back to sign in')}
          </Link>
        </div>
      </Screen>
    )

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    run(async () => {
      try {
        await api('/api/auth/verify', {
          method: 'POST',
          body: { token, password: needPassword ? password : undefined },
        })
        setDone(true)
      } catch (err) {
        if (err instanceof RequestError && err.error.kind === 'password_required') setNeedPassword(true)
        if (err instanceof RequestError && err.error.kind === 'invalid_token') return setInvalid(true)
        throw err
      }
    })
  }
  return (
    <Screen title={title}>
      <form onSubmit={submit} className={cardClass}>
        {needPassword && (
          <>
            <p className="text-sm text-muted-foreground">
              {t('Enter your password to confirm your email and sign in.')}
            </p>
            <Input
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t('Password')}
              aria-label={t('Password')}
            />
          </>
        )}
        <ErrorLine error={error} />
        <Submit busy={busy}>{t('Confirm my email')}</Submit>
      </form>
    </Screen>
  )
}

function ResetPassword() {
  const token = useLinkToken()
  const [password, setPassword] = useState('')
  const [done, setDone] = useState(false)
  const { busy, error, run } = useAction()
  const title = t('Choose a new password')

  if (done) return <Done title={title} message={t('Password changed. Other devices were signed out.')} />
  if (token === null)
    return (
      <Screen title={title}>
        <div className={cardClass}>
          <p role="alert" className="text-sm text-destructive">
            {t('This link has expired or was already used.')}
          </p>
          <Link to="/forgot" className={linkClass}>
            {t('Reset your password')}
          </Link>
        </div>
      </Screen>
    )

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    run(async () => {
      await api('/api/auth/reset', { method: 'POST', body: { token, password } })
      setDone(true)
    })
  }
  return (
    <Screen title={title}>
      <form onSubmit={submit} className={cardClass}>
        <Input
          type="password"
          autoComplete="new-password"
          autoFocus
          required
          minLength={8}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t('New password (8+ characters)')}
          aria-label={t('New password')}
        />
        <ErrorLine error={error} />
        <Submit busy={busy}>{t('Save and sign in')}</Submit>
      </form>
    </Screen>
  )
}
