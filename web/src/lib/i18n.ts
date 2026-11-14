import { getPrefs } from './prefs'
import es from '@/locales/es'

// UI translations, gettext style: the English text is the key, so code reads
// naturally and a missing translation falls back to English. The language is
// fixed for the page's lifetime (changing it reloads), so t() also works in
// module-level constants. src/lib/i18n.test.ts checks every t('…') has a
// Spanish entry.
export type Lang = 'en' | 'es'
type Vars = Record<string, string | number>

function resolve(): Lang {
  const p = getPrefs().language
  if (p === 'en' || p === 'es') return p
  return navigator.language.toLowerCase().startsWith('es') ? 'es' : 'en'
}

export const lang: Lang = resolve()

/** Each language's name in itself, as language pickers conventionally show it: never translated. */
export const languageNames: Record<Lang, string> = { en: 'English', es: 'Español' }
document.documentElement.lang = lang
const dict: Record<string, string> = lang === 'es' ? es : {}

/** Translates text; {name} placeholders are filled from vars. */
export function t(text: string, vars?: Vars): string {
  const s = dict[text] ?? text
  return vars ? s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m)) : s
}

/** Singular or plural by n; both forms are keys, and {n} is filled in. */
export function tn(n: number, one: string, other: string, vars?: Vars): string {
  return t(n === 1 ? one : other, { n, ...vars })
}
