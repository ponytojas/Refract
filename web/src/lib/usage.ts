import type { Allowance, Model, ModelUsage } from '../api'

/** The first instant of the month containing now, in the browser's time zone, as Unix seconds. */
export function monthStart(now: Date): number {
  return Math.floor(new Date(now.getFullYear(), now.getMonth(), 1).getTime() / 1000)
}

/** A token count for people: 1234567 → "1.2M" in English, "1,2 M" in Spanish. */
export function compact(n: number, lang: string): string {
  return new Intl.NumberFormat(lang, { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}

/**
 * The name to show for a recorded model. Usage keeps the slug the assistant
 * asked for ("<provider id>:<model>" for API providers), so a model still on
 * offer shows its name and provider; one no longer listed shows its model id.
 */
export function modelLabel(slug: string, models: Model[]): { name: string; provider?: string } {
  const m = models.find((x) => x.slug === slug)
  if (m) return { name: m.display_name || slug, provider: m.provider_name }
  const i = slug.indexOf(':')
  return { name: i >= 0 ? slug.slice(i + 1) : slug }
}

/** Input plus output tokens: cached and reasoning tokens are parts of them, not additions. */
export const totalTokens = (u: ModelUsage) => u.input_tokens + u.output_tokens

/** The share of the included AI left this month, as a whole percentage from 0 to 100. */
export function allowanceLeft(a: Allowance): number {
  if (a.limit_usd <= 0) return 0
  return Math.round(Math.min(1, Math.max(0, 1 - a.spent_usd / a.limit_usd)) * 100)
}

/** The day the included AI renews, for people: "1 December" in English, "1 de diciembre" in Spanish. */
export function renewalDate(a: Allowance, lang: string): string {
  return new Date(a.resets_at * 1000).toLocaleDateString(lang, { day: 'numeric', month: 'long' })
}

/** The calls made with the account's own keys or ChatGPT plan: everything but the included AI. */
export const ownUsage = (rows: ModelUsage[]) => rows.filter((u) => !u.included)
