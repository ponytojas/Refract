import { useState } from 'react'
import { Loader2, MailWarning } from 'lucide-react'
import { toast } from 'sonner'
import { api, errorText } from '@/api'
import { Button } from '@/components/ui/button'
import { useAccount } from '@/components/useAccount'
import { t } from '@/lib/i18n'

/** A strip above the project list while the account's email is unconfirmed (creating and importing need it). */
export default function VerifyEmailNotice() {
  const { account } = useAccount()
  const [busy, setBusy] = useState(false)
  if (!account || account.email_verified) return null

  async function resend() {
    setBusy(true)
    try {
      await api('/api/account/verify-email', { method: 'POST' })
      toast.success(t('Sent. The link works for 24 hours.'))
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div
      role="status"
      className="mx-5 mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
    >
      <MailWarning className="size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden />
      <span className="mr-auto">
        {t('Confirm your email address to create or import projects. We sent a link to {email}.', {
          email: account.email,
        })}
      </span>
      {account.mail && (
        <Button type="button" size="sm" variant="outline" disabled={busy} onClick={resend}>
          {busy && <Loader2 className="animate-spin" />} {t('Send it again')}
        </Button>
      )}
    </div>
  )
}
