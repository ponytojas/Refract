import { useEffect, useState, type FormEvent } from 'react'
import { ArrowLeft, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api, errorText, RequestError, type ModelUsage } from '@/api'
import { useMe } from '@/components/AccountMenu'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { lang, t } from '@/lib/i18n'
import { Link, pageReady, reloadTo } from '@/lib/router'
import { monthStart, totalTokens } from '@/lib/usage'

const accountsURL = '/api/admin/accounts'

type Account = { id: string; email: string; has_password: boolean; email_verified: boolean; is_admin: boolean }
type Report = { models: ModelUsage[]; projects: { id: string; name: string; trashed: boolean; bytes: number }[] }
type AccountAction = 'rename' | 'reset' | 'signout' | 'unlock' | 'remove'
type Action =
  | { kind: AccountAction; account: Account }
  | { kind: 'add'; account?: never }
  | { kind: 'unlock_all'; account?: never }

const accountActionLabels = {
  rename: () => t('Change email'),
  reset: () => t('Reset password'),
  signout: () => t('Sign out everywhere'),
  unlock: () => t('Clear lockout'),
  remove: () => t('Delete account'),
}

const actionDescriptions = {
  rename: (email: string) =>
    t('Change the sign-in address for {email} and revoke their Refract sessions and email links.', { email }),
  reset: (email: string) => t('Generate a new password for {email} and end all of their Refract sessions.', { email }),
  signout: (email: string) => t('End all Refract sessions for {email}.', { email }),
  unlock: (email: string) => t('Clear failed sign-in attempts for {email}.', { email }),
  remove: (email: string) =>
    t('Permanently delete {email}, their projects, history, chats and connections.', { email }),
}

export default function Admin() {
  const me = useMe()
  const [accounts, setAccounts] = useState<Account[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [version, setVersion] = useState(0)
  const [selected, setSelected] = useState('')
  const [action, setAction] = useState<Action | null>(null)
  const account = accounts?.find((a) => a.id === selected) ?? accounts?.[0]

  useEffect(() => {
    const controller = new AbortController()
    api<Account[]>(accountsURL, { signal: controller.signal })
      .then((items) => {
        setAccounts(items)
        setLoadError('')
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        setAccounts(null)
        setLoadError(
          error instanceof RequestError && error.status === 404 ? t('Page not found') : t('Could not load accounts.'),
        )
      })
      .finally(() => {
        if (!controller.signal.aborted) pageReady()
      })
    return () => controller.abort()
  }, [version])

  return (
    <div className="h-dvh overflow-y-auto bg-background">
      <main className="mx-auto max-w-6xl space-y-6 px-5 py-8 sm:px-8">
        <Link to="/" className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft aria-hidden="true" className="size-4" /> {t('All projects')}
        </Link>
        {loadError ? (
          <p role="alert">{loadError}</p>
        ) : (
          <>
            <header className="flex flex-wrap items-center justify-between gap-4">
              <h1 className="text-2xl font-semibold tracking-tight">{t('Account administration')}</h1>
              {accounts && (
                <div className="flex flex-wrap gap-2">
                  <Button variant="outline" onClick={() => setAction({ kind: 'unlock_all' })}>
                    {t('Clear all lockouts')}
                  </Button>
                  <Button onClick={() => setAction({ kind: 'add' })}>{t('Add account')}</Button>
                </div>
              )}
            </header>
            {!accounts ? (
              <p role="status" className="text-sm text-muted-foreground">
                {t('Loading…')}
              </p>
            ) : (
              <div className="grid items-start gap-5 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                <section className="overflow-hidden rounded-xl border bg-card" aria-label={t('Accounts')}>
                  <ul className="divide-y">
                    {accounts.map((a) => (
                      <li key={a.id}>
                        <button
                          type="button"
                          onClick={() => setSelected(a.id)}
                          aria-pressed={account?.id === a.id}
                          className="flex w-full flex-wrap items-center justify-between gap-2 px-4 py-3 text-left text-sm hover:bg-muted aria-pressed:bg-muted"
                        >
                          <span className="break-all">{a.email}</span>
                          {a.is_admin && <Badge variant="secondary">{t('Administrator')}</Badge>}
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
                {account && (
                  <section className="min-w-0 space-y-4 rounded-xl border bg-card p-5">
                    <div className="space-y-1">
                      <h2 className="text-lg font-medium break-all">{account.email}</h2>
                      <p className="text-sm text-muted-foreground">
                        {account.email_verified ? t('Confirmed') : t('Not confirmed')}
                        {' · '}
                        {account.has_password ? t('Password set') : t('No password')}
                      </p>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {(Object.keys(accountActionLabels) as AccountAction[]).map((kind) => (
                        <Button
                          key={kind}
                          size="sm"
                          variant={kind === 'remove' ? 'destructive' : 'outline'}
                          onClick={() => setAction({ kind, account })}
                        >
                          {accountActionLabels[kind]()}
                        </Button>
                      ))}
                    </div>
                    <AccountReport key={`${account.id}:${version}`} id={account.id} />
                  </section>
                )}
              </div>
            )}
          </>
        )}
      </main>
      {action && (
        <ActionDialog
          action={action}
          self={!!action.account && action.account.id === me?.id}
          close={() => setAction(null)}
          refresh={() => setVersion((n) => n + 1)}
        />
      )}
    </div>
  )
}

function AccountReport({ id }: { id: string }) {
  const [report, setReport] = useState<Report | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    const query = new URLSearchParams({ id, since: String(monthStart(new Date())) })
    api<Report>(`${accountsURL}/report?${query}`, { signal: controller.signal }).then(setReport, () => {
      if (!controller.signal.aborted) setFailed(true)
    })
    return () => controller.abort()
  }, [id])

  if (!report) {
    return (
      <p role={failed ? 'alert' : 'status'} className="text-sm text-muted-foreground">
        {failed ? t('Could not load account usage and storage.') : t('Loading…')}
      </p>
    )
  }
  const number = new Intl.NumberFormat(lang)
  return (
    <div className="space-y-5 border-t pt-4">
      <section className="space-y-2">
        <h3 className="font-medium">{t('Usage this month')}</h3>
        {report.models.length ? (
          <dl className="divide-y text-sm">
            {report.models.map((u) => (
              <div key={u.model} className="flex justify-between gap-3 py-2">
                <dt className="break-all">{u.model}</dt>
                <dd className="shrink-0 tabular-nums">
                  {t('{count} tokens', { count: number.format(totalTokens(u)) })}
                </dd>
              </div>
            ))}
          </dl>
        ) : (
          <p className="text-sm text-muted-foreground">{t('No AI requests this month.')}</p>
        )}
      </section>
      <section className="space-y-2">
        <h3 className="font-medium">{t('Project disk space')}</h3>
        {report.projects.length ? (
          <dl className="divide-y text-sm">
            {report.projects.map((p) => (
              <div key={p.id} className="flex justify-between gap-3 py-2">
                <dt className="break-all">
                  {p.name}
                  {p.trashed && (
                    <Badge className="ml-2" variant="secondary">
                      {t('In the trash')}
                    </Badge>
                  )}
                </dt>
                <dd className="shrink-0 tabular-nums">{t('{count} bytes', { count: number.format(p.bytes) })}</dd>
              </div>
            ))}
          </dl>
        ) : (
          <p className="text-sm text-muted-foreground">{t('This account has no projects.')}</p>
        )}
      </section>
    </div>
  )
}

function ActionDialog({
  action,
  self,
  close,
  refresh,
}: {
  action: Action
  self: boolean
  close: () => void
  refresh: () => void
}) {
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [password, setPassword] = useState('')
  const needsEmail = action.kind === 'rename' || action.kind === 'add'
  const title =
    action.kind === 'add'
      ? t('Add account')
      : action.kind === 'unlock_all'
        ? t('Clear all lockouts')
        : accountActionLabels[action.kind]()
  const description =
    action.kind === 'add'
      ? t('Create an account and generate its first password.')
      : action.kind === 'unlock_all'
        ? t('Clear failed sign-in attempts for every account and network address.')
        : actionDescriptions[action.kind](action.account.email)

  function done() {
    setPassword('')
    close()
    if (
      self &&
      (action.kind === 'remove' || action.kind === 'signout' || action.kind === 'reset' || action.kind === 'rename')
    ) {
      void reloadTo('/')
      return
    }
    refresh()
  }

  async function copyPassword() {
    try {
      await navigator.clipboard.writeText(password)
      toast.success(t('Password copied.'))
    } catch {
      toast.error(t('Could not copy the password. Select it to copy it manually.'))
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      const result = await api<{ password?: string }>(accountsURL, {
        method: 'POST',
        body: { action: action.kind, id: action.account?.id, email, confirmed: true },
      })
      if (result.password) {
        setPassword(result.password)
      } else {
        toast.success(t('Account administration action completed.'))
        done()
      }
    } catch (error) {
      const kind = error instanceof RequestError ? error.error.kind : ''
      if (kind === 'account_removal_partial') {
        toast.error(errorText(error))
        close()
        refresh()
        return
      }
      const message =
        kind === 'admin_account_exists'
          ? t('An account already has this email address.')
          : kind === 'admin_invalid_email'
            ? t('Enter a valid email address.')
            : kind === 'admin_account_busy'
              ? t('The account’s projects are changing. Try again when they are ready.')
              : t('Could not complete this action.')
      toast.error(message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) {
          if (password) done()
          else close()
        }
      }}
    >
      <AlertDialogContent>
        {password ? (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>{t('Generated password')}</AlertDialogTitle>
              <AlertDialogDescription>
                {t('Copy this password before closing. Share it privately with the account holder.')}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <Input
              readOnly
              value={password}
              aria-label={t('Generated password')}
              onFocus={(e) => e.currentTarget.select()}
            />
            <AlertDialogFooter>
              <Button onClick={copyPassword}>{t('Copy password')}</Button>
              <Button variant="outline" onClick={done}>
                {t('Done')}
              </Button>
            </AlertDialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="grid gap-5">
            <AlertDialogHeader>
              <AlertDialogTitle>{title}</AlertDialogTitle>
              <AlertDialogDescription>{description}</AlertDialogDescription>
            </AlertDialogHeader>
            {needsEmail && (
              <label className="grid gap-2 text-sm">
                {t('Email')}
                <Input
                  type="email"
                  autoComplete="off"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </label>
            )}
            <AlertDialogFooter>
              <AlertDialogCancel disabled={busy}>{t('Cancel')}</AlertDialogCancel>
              <Button type="submit" disabled={busy} variant={action.kind === 'remove' ? 'destructive' : 'default'}>
                {busy && <Loader2 aria-hidden="true" className="animate-spin" />}
                {title}
              </Button>
            </AlertDialogFooter>
          </form>
        )}
      </AlertDialogContent>
    </AlertDialog>
  )
}
