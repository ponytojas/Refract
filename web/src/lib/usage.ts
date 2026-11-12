import type { Model, ModelUsage } from '../api'

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
