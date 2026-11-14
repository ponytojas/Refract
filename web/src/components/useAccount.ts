import { useEffect, useSyncExternalStore } from 'react'
import { toast } from 'sonner'
import { api, errorText } from '@/api'
import { useMe } from '@/components/AccountMenu'
import { t } from '@/lib/i18n'

export type Account = {
  email: string
  has_password: boolean
  email_verified: boolean
  lang: '' | 'en' | 'es'
  projects: number // owned, the trash included
  mail: boolean // the server can send email
  oidc: string // the identity provider's name, or ""
  recent_sign_in: boolean // signed in within the last 10 minutes
}

// One copy of the account for every view that shows it (the notice above the
// project list, Settings → Account), so they fetch once and change together.
type State = { account: Account | null; sending: boolean }
let state: State = { account: null, sending: false }
let loading: Promise<void> | null = null
const listeners = new Set<() => void>()

function set(next: Partial<State>) {
  state = { ...state, ...next }
  listeners.forEach((l) => l())
}
const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

/** Fetches the account again (GET /api/account); calls that overlap share one request. */
export function reloadAccount(): Promise<void> {
  loading ??= api<Account>('/api/account')
    .then(
      (account) => set({ account }),
      () => {},
    )
    .finally(() => (loading = null))
  return loading
}

/** Asks for a new confirmation link and says how it went. */
export async function resendVerification() {
  if (state.sending) return
  set({ sending: true })
  try {
    await api('/api/account/verify-email', { method: 'POST' })
    toast.success(t('Sent. The link works for 24 hours.'))
  } catch (e) {
    toast.error(errorText(e))
  } finally {
    set({ sending: false })
  }
}

/** The signed-in account, for accounts Refract signs in itself. */
export function useAccount() {
  const me = useMe()
  const { account, sending } = useSyncExternalStore(subscribe, () => state)
  const canSignOut = me?.can_sign_out
  useEffect(() => {
    if (canSignOut && !state.account) void reloadAccount()
  }, [canSignOut])
  return { account, sending, reload: reloadAccount, resend: resendVerification }
}
