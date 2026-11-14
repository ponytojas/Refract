// Run with: node --test web/src/lib/account.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { linkToken } from './account.ts'

test('reads the token from the fragment', () => {
  assert.equal(linkToken('#token=abc'), 'abc')
  assert.equal(linkToken('token=abc'), 'abc')
  assert.equal(linkToken('#a=1&token=x-_Y'), 'x-_Y')
})

test('anything else is no token', () => {
  assert.equal(linkToken(''), null)
  assert.equal(linkToken('#'), null)
  assert.equal(linkToken('#token='), null)
  assert.equal(linkToken('#other=abc'), null)
  assert.equal(linkToken('#%%%'), null)
})
