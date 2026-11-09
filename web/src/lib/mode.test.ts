import assert from 'node:assert/strict'
import { test } from 'node:test'

import { chatgptOffered, setMode } from './mode.ts'

test('the ChatGPT plan is offered only when self-hosted', () => {
  assert.equal(chatgptOffered(), true) // the default, also when /api/me fails
  setMode('saas')
  assert.equal(chatgptOffered(), false)
  setMode('nonsense') // ignored
  assert.equal(chatgptOffered(), false)
  setMode('selfhost')
  assert.equal(chatgptOffered(), true)
})
