// Run with: node --test web/src/lib/i18n.test.ts
// Every string passed to t()/tn() as a literal needs a Spanish entry, and
// every Spanish entry must still be used.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

const src = new URL('..', import.meta.url).pathname
const str = String.raw`'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|` + '`((?:[^`\\\\$]|\\\\.)*)`'
const tCall = new RegExp(String.raw`\bt\(\s*(?:${str})`, 'g')
// tn's first argument is the count: any expression, commas allowed inside
// (nested) parentheses, as in tn(Math.min(a, b), …).
const arg = String.raw`(?:[^,()]|\((?:[^()]|\([^()]*\))*\))+`
const tnCall = new RegExp(String.raw`\btn\(\s*${arg},\s*(?:${str})\s*,\s*(?:${str})`, 'g')
const unescape = (s: string) => s.replace(/\\(.)/g, (_, c: string) => ({ n: '\n', t: '\t' })[c] ?? c)

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) return f === 'locales' ? [] : files(p)
    return /\.tsx?$/.test(f) && !f.endsWith('.test.ts') && f !== 'i18n.ts' ? [p] : []
  })
}

const used = new Map<string, string>() // key → first file using it
for (const f of files(src)) {
  const code = readFileSync(f, 'utf8')
  for (const m of code.matchAll(tCall)) used.set(unescape(m[1] ?? m[2] ?? m[3]), f)
  for (const m of code.matchAll(tnCall)) {
    used.set(unescape(m[1] ?? m[2] ?? m[3]), f)
    used.set(unescape(m[4] ?? m[5] ?? m[6]), f)
  }
}

const es: Record<string, string> = {}
const dir = join(src, 'locales/es')
for (const f of readdirSync(dir).filter((f) => f !== 'index.ts')) Object.assign(es, (await import(join(dir, f))).default)

test('every UI string has a Spanish translation', () => {
  const missing = [...used.keys()].filter((k) => !(k in es)).map((k) => `${JSON.stringify(k)} (${used.get(k)!.replace(src, '')})`)
  assert.deepEqual(missing, [])
})

test('no stale translations', () => {
  assert.deepEqual(Object.keys(es).filter((k) => !used.has(k)), [])
})

test('placeholders survive translation', () => {
  const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join()
  assert.deepEqual(Object.entries(es).filter(([k, v]) => vars(k) !== vars(v)).map(([k]) => k), [])
})
