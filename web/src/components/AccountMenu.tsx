import { useEffect, useState } from 'react'
import { Check, ChevronsUpDown, ExternalLink, Gauge, LogOut, Monitor, Moon, Settings, Sun } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { api, type Allowance, type ChatGPTStatus, type Model, type ModelUsage } from '@/api'
import { openSettings } from '@/lib/prefs'
import { setTheme, useTheme, type Theme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { lang, t } from '@/lib/i18n'
import { allowanceLeft, compact, modelLabel, ownUsage, renewalDate, totalTokens } from '@/lib/usage'
import { useMonthUsage } from './useMonthUsage'
import { chatgptOffered } from '@/lib/mode'

const themes: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: t('Light'), icon: Sun },
  { value: 'dark', label: t('Dark'), icon: Moon },
  { value: 'system', label: t('System'), icon: Monitor },
]

// Bottom-left account chip: ChatGPT identity, settings, theme.
type Me = { id: string; email: string; can_sign_out: boolean }
let mePromise: Promise<Me | null> | null = null
export function useMe() {
  const [me, setMe] = useState<Me | null>(null)
  useEffect(() => {
    mePromise ??= api<Me>('/api/me').catch(() => null)
    mePromise.then(setMe)
  }, [])
  return me
}

export default function AccountMenu({ status, compact }: { status: ChatGPTStatus | null; compact?: boolean }) {
  const { theme } = useTheme()
  const me = useMe()
  // With several users, the chip shows who is signed in to Refract; alone, the ChatGPT account.
  const chatgpt = chatgptOffered()
  const local = !me || me.email === 'local'
  const email = !local ? me.email : chatgpt && status?.status.signed_in ? status.status.email : undefined
  const [open, setOpen] = useState(false)
  const { usage, allowance, models } = useMonthUsage(open)
  const own = usage && ownUsage(usage)
  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger
        aria-label={t('Account menu')}
        onMouseDown={(e) => e.preventDefault()}
        style={{ viewTransitionName: 'account' }}
        className="flex min-w-0 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors duration-150 outline-none hover:bg-foreground/[0.05] focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <span className="flex size-7 shrink-0 items-center justify-center rounded-full border bg-card text-xs font-semibold uppercase shadow-xs">
          {email?.[0] ?? 'R'}
        </span>
        {!compact && (
          <span className="min-w-0">
            <span className="flex items-center gap-1 truncate text-sm font-medium">
              {email ?? (chatgpt ? t('Not signed in') : t('Local account'))}{' '}
              <ChevronsUpDown className="size-3 shrink-0 text-muted-foreground" />
            </span>
            <span className="block truncate text-xs text-muted-foreground">
              {status?.status.plan_granted
                ? t('Using ChatGPT plan')
                : status?.ai_ready
                  ? t('Using API providers')
                  : chatgpt
                    ? t('ChatGPT not connected')
                    : t('No AI connected')}
            </span>
          </span>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="w-60">
        {allowance && <IncludedAllowance allowance={allowance} />}
        {/* With the included AI, the account's own keys show only once they have been used. */}
        {status?.ai_ready && (!allowance || !!own?.length) && (
          <MonthUsage usage={own} models={models} label={allowance ? t('With your own keys') : undefined} />
        )}
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{t('Theme')}</DropdownMenuLabel>
          {themes.map((opt) => (
            <DropdownMenuItem key={opt.value} onClick={() => setTheme(opt.value)}>
              <opt.icon /> {opt.label}
              <Check className={cn('ml-auto', theme !== opt.value && 'invisible')} />
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => openSettings()}>
          <Settings /> {t('Settings')}
        </DropdownMenuItem>
        {status?.status.signed_in && (
          <DropdownMenuItem render={<a href={status.manage_usage} target="_blank" rel="noreferrer" />}>
            <ExternalLink /> {t('Manage ChatGPT usage')}
          </DropdownMenuItem>
        )}
        {me?.can_sign_out && (
          <DropdownMenuItem
            onClick={() => api('/api/auth/logout', { method: 'POST' }).finally(() => location.assign('/'))}
          >
            <LogOut /> {t('Sign out')}
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// This month's tokens with the account's own AI (API keys or ChatGPT plan):
// the total, and per model in a submenu that opens on hover or with the keyboard.
function MonthUsage({ usage, models, label }: { usage: ModelUsage[] | null; models: Model[]; label?: string }) {
  const n = (x: number) => compact(x, lang)
  const total = usage?.reduce((sum, u) => sum + totalTokens(u), 0) ?? 0
  return (
    <>
      <DropdownMenuGroup>
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
          {label ?? t('Usage this month')}
        </DropdownMenuLabel>
        {!usage?.length ? (
          <DropdownMenuItem onClick={() => openSettings('assistant')}>
            <Gauge />
            <span className="text-muted-foreground">{usage ? t('No AI requests yet') : t('Loading…')}</span>
          </DropdownMenuItem>
        ) : (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Gauge />
              <span className="tabular-nums">{t('{n} tokens', { n: n(total) })}</span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-80">
              <DropdownMenuGroup>
                <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
                  {t('By model, as each provider reported it')}
                </DropdownMenuLabel>
                {usage.map((u) => {
                  const { name, provider } = modelLabel(u.model, models)
                  return (
                    <div key={u.model} className="flex items-baseline gap-3 px-2 py-1.5 text-sm">
                      <span className="min-w-0 flex-1">
                        <span className="flex items-baseline gap-1.5">
                          <span className="truncate">{name}</span>
                          {provider && <span className="truncate text-xs text-muted-foreground">{provider}</span>}
                        </span>
                        <span className="block text-xs text-muted-foreground">
                          {t('{n} input', { n: n(u.input_tokens) })} · {t('{n} output', { n: n(u.output_tokens) })}
                        </span>
                      </span>
                      <span className="shrink-0 tabular-nums">{n(totalTokens(u))}</span>
                    </div>
                  )
                })}
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => openSettings('assistant')}>
                <Settings /> {t('Details in Settings')}
              </DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
      </DropdownMenuGroup>
      <DropdownMenuSeparator />
    </>
  )
}

// What is left of this month's included AI, as a bar that empties.
function IncludedAllowance({ allowance }: { allowance: Allowance }) {
  const pct = allowanceLeft(allowance)
  const renews = renewalDate(allowance, lang)
  return (
    <>
      <DropdownMenuGroup>
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{t('Refract AI')}</DropdownMenuLabel>
        <div className="px-2 pt-1 pb-2">
          <div className="text-sm tabular-nums">{pct === 0 ? t('Used up this month') : t('{n}% left', { n: pct })}</div>
          <div
            role="progressbar"
            aria-label={t('Included AI left this month')}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={pct}
            className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-foreground/10"
          >
            <div
              className={cn(
                'h-full rounded-full transition-[width] duration-300 ease-out',
                pct > 20 ? 'bg-foreground/70' : 'bg-destructive',
              )}
              style={{ width: `${pct}%` }}
            />
          </div>
          <div className="mt-1.5 text-xs text-muted-foreground">{t('Renews {date}', { date: renews })}</div>
        </div>
        {pct === 0 && (
          <DropdownMenuItem onClick={() => openSettings('assistant')}>
            <Settings /> {t('Add your own API key')}
          </DropdownMenuItem>
        )}
      </DropdownMenuGroup>
      <DropdownMenuSeparator />
    </>
  )
}
