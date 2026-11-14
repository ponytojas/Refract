import { useCallback, useEffect, useState } from 'react'
import { api } from '@/api'
import { useMe } from '@/components/AccountMenu'

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

/** The signed-in account (GET /api/account), for accounts Refract signs in itself. */
export function useAccount(): { account: Account | null; reload: () => void } {
  const me = useMe()
  const [account, setAccount] = useState<Account | null>(null)
  const [version, setVersion] = useState(0)
  const reload = useCallback(() => setVersion((v) => v + 1), [])
  useEffect(() => {
    if (!me?.can_sign_out) return
    let live = true
    api<Account>('/api/account').then(
      (a) => live && setAccount(a),
      () => {},
    )
    return () => {
      live = false
    }
  }, [me?.can_sign_out, version])
  return { account, reload }
}
