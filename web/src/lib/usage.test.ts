// Run with: node --test web/src/lib/usage.test.ts
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { allowanceLeft, compact, modelLabel, monthStart, ownUsage, renewalDate, totalTokens } from './usage.ts'

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

test('the included AI shows what is left, never below 0 or above 100', () => {
  const at = (spent: number) => allowanceLeft({ limit_usd: 5, spent_usd: spent, resets_at: 0 })
  assert.equal(at(0), 100)
  assert.equal(at(1.9), 62)
  assert.equal(at(4.3), 14)
  assert.equal(at(5), 0)
  assert.equal(at(5.2), 0) // a running turn can end slightly over the limit
  assert.equal(allowanceLeft({ limit_usd: 0, spent_usd: 0, resets_at: 0 }), 0)
})

test('the renewal date is the day the allowance resets', () => {
  const a = { limit_usd: 5, spent_usd: 1, resets_at: new Date(2026, 11, 1).getTime() / 1000 }
  assert.equal(renewalDate(a, 'en'), 'December 1')
  assert.equal(renewalDate(a, 'es'), '1 de diciembre')
})

test('own usage leaves out the included AI', () => {
  const row = (model: string, included?: boolean) => ({
    model,
    calls: 1,
    input_tokens: 1,
    cached_tokens: 0,
    output_tokens: 1,
    reasoning_tokens: 0,
    included,
  })
  assert.deepEqual(
    ownUsage([row('refract', true), row('mine'), row('chatgpt', false)]).map((u) => u.model),
    ['mine', 'chatgpt'],
  )
})
