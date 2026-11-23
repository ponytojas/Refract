// Run with: node --test web/src/lib/flush.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { flushPending, onFlush } from './flush.ts'

test('nothing registered: everything is saved', async () => {
  assert.equal(await flushPending(), true)
})

test('waits for every flush and reports one that could not save', async () => {
  let saved = false
  const offA = onFlush(async () => {
    await new Promise((r) => setTimeout(r, 20))
    saved = true
    return true
  })
  assert.equal(await flushPending(), true)
  assert.equal(saved, true)
  const offB = onFlush(async () => false)
  assert.equal(await flushPending(), false)
  offB()
  assert.equal(await flushPending(), true)
  offA()
})

test('a failing or hung flush counts as unsaved', async () => {
  const offFail = onFlush(() => Promise.reject(new Error('network')))
  assert.equal(await flushPending(), false)
  offFail()
  const offHung = onFlush(() => new Promise<boolean>(() => {}))
  const start = Date.now()
  assert.equal(await flushPending(50), false)
  assert.ok(Date.now() - start < 1000)
  offHung()
})
