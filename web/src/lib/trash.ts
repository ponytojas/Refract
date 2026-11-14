// Kept apart from time.ts, which imports i18n and can't load under node --test.

/** Whole days until a project in the trash is deleted for good: never less than 1, so the last day doesn't read "0 days". */
export function daysLeft(purgeAt: string, now = Date.now()): number {
  return Math.max(1, Math.ceil((Date.parse(purgeAt) - now) / 86_400_000))
}
