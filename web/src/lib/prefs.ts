import { useSyncExternalStore } from 'react'

// Per-browser UI preferences (localStorage). The theme lives in theme.ts: it must apply before first paint.
export type Prefs = {
  fontSize: 'small' | 'normal' | 'large'
  wrap: boolean
  wordCountTitles: boolean
  wordCount: boolean
  chatBubble: boolean // collapsed assistant: a bubble instead of the input bar
  chatHeight: number // px of the open chat sheet; 0 = default
  chatDocked: boolean // the assistant as a side panel instead of floating over the editor
  model: string // assistant model slug; '' = the server's default
  effort: string // reasoning effort; '' = the model's default
  language: 'system' | 'en' | 'es'
}

const KEY = 'refract-prefs'
const defaults: Prefs = {
  fontSize: 'normal',
  wrap: true,
  wordCount: false,
  wordCountTitles: true,
  chatBubble: false,
  chatHeight: 0,
  chatDocked: false,
  model: '',
  effort: '',
  language: 'system',
}
const listeners = new Set<() => void>()

function load(): Prefs {
  try {
    return { ...defaults, ...JSON.parse(localStorage.getItem(KEY) ?? '{}') }
  } catch {
    return defaults
  }
}

let prefs = load()

export const fontSizes: Record<Prefs['fontSize'], string> = { small: '12.5px', normal: '13.5px', large: '15px' }

function apply() {
  document.documentElement.style.setProperty('--editor-font-size', fontSizes[prefs.fontSize] ?? fontSizes.normal)
}
apply()

export function setPref<K extends keyof Prefs>(key: K, value: Prefs[K]) {
  prefs = { ...prefs, [key]: value }
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs))
  } catch {
    /* private mode: per-session only */
  }
  apply()
  listeners.forEach((l) => l())
}

export const getPrefs = () => prefs

export function usePrefs(): Prefs {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => prefs,
  )
}

// The settings dialog is opened from several places (account menu, assistant errors, /settings).
let settingsSection: string | null = null
const settingsListeners = new Set<() => void>()
export function openSettings(section: string | null = 'general') {
  settingsSection = section
  settingsListeners.forEach((l) => l())
}
export function useSettingsSection(): string | null {
  return useSyncExternalStore(
    (l) => {
      settingsListeners.add(l)
      return () => settingsListeners.delete(l)
    },
    () => settingsSection,
  )
}
