import assert from 'node:assert/strict'
import { test } from 'node:test'

import { failureText, settled, type LoginState } from './chatgpt-login.ts'

const id = (s: string) => s

test('each failure kind has its own explanation', () => {
  const kinds: LoginState['kind'][] = ['cancelled', 'timeout', 'port_busy', 'other_account']
  const texts = kinds.map((kind) => failureText({ state: 'failed', kind }, id))
  assert.equal(new Set(texts).size, kinds.length)
  assert.match(failureText({ state: 'failed', kind: 'port_busy' }, id), /1455/)
})

test("an unexpected failure shows the server's message, or a generic one", () => {
  assert.equal(failureText({ state: 'failed', kind: 'other', message: 'not allowed' }, id), 'not allowed')
  assert.equal(failureText({ state: 'failed', kind: 'other' }, id), 'Sign-in did not complete.')
})

test('only a pending sign-in keeps polling', () => {
  assert.equal(settled({ state: 'pending' }), false)
  for (const state of ['idle', 'done', 'failed'] as const) assert.equal(settled({ state }), true)
})
