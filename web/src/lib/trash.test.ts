// Run with: node --test web/src/lib/trash.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { daysLeft } from './trash.ts'

const day = 86_400_000
const at = '2026-02-01T00:00:00Z'
const t0 = Date.parse(at)

test('counts whole days, rounding up', () => {
  assert.equal(daysLeft(at, t0 - 30 * day), 30)
  assert.equal(daysLeft(at, t0 - 29.5 * day), 30)
  assert.equal(daysLeft(at, t0 - 2 * day), 2)
  assert.equal(daysLeft(at, t0 - 1.1 * day), 2)
})

test('never drops below one day', () => {
  assert.equal(daysLeft(at, t0 - 1000), 1)
  assert.equal(daysLeft(at, t0), 1)
  assert.equal(daysLeft(at, t0 + 5 * day), 1)
})
