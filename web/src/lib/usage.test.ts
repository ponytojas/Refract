// Run with: node --test web/src/lib/usage.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { compact, modelLabel, monthStart, totalTokens } from './usage.ts'

const models = [
  {
    slug: 'p1:claude-haiku',
    display_name: 'Claude Haiku',
    description: '',
    default_effort: '',
    efforts: [],
    provider_name: 'Anthropic',
  },
]

test('a listed model shows its name and provider', () => {
  assert.deepEqual(modelLabel('p1:claude-haiku', models), { name: 'Claude Haiku', provider: 'Anthropic' })
})

test('a model no longer listed shows its model id, without the provider prefix', () => {
  assert.deepEqual(modelLabel('p2:gpt-mini', models), { name: 'gpt-mini' })
  assert.deepEqual(modelLabel('gpt-5', models), { name: 'gpt-5' })
})

test('the month starts on day 1 at local midnight', () => {
  const s = monthStart(new Date(2026, 10, 12, 15, 30))
  assert.equal(s, new Date(2026, 10, 1).getTime() / 1000)
})

test('token counts are compact', () => {
  assert.equal(compact(950, 'en'), '950')
  assert.equal(compact(1_234_567, 'en'), '1.2M')
})

test('the total is input plus output', () => {
  const u = { model: 'm', calls: 1, input_tokens: 100, cached_tokens: 60, output_tokens: 40, reasoning_tokens: 25 }
  assert.equal(totalTokens(u), 140)
})
