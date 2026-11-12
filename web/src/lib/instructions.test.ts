// Run with: node --test web/src/lib/instructions.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { forQuickAction, instructionsTemplate, instructionsText } from './instructions.ts'

test('the template says nothing to the assistant until the author writes', () => {
  const template = instructionsTemplate((s) => s)
  assert.match(template, /ask the assistant to remember/)
  assert.equal(instructionsText(template), '')
  assert.equal(forQuickAction(template), undefined)
  assert.equal(forQuickAction(template + 'Use British spelling.\n'), 'Use British spelling.')
})

test('comments are dropped, even an unterminated one', () => {
  assert.equal(instructionsText('a <!-- x --> b <!-- open'), 'a  b')
  assert.equal(forQuickAction(undefined), undefined)
})

test('what is sent stays under the server limit', () => {
  const sent = forQuickAction('€'.repeat(10000))!
  assert.equal(sent.length, 4096)
  assert.ok(new TextEncoder().encode(sent).length <= 16 << 10)
})
