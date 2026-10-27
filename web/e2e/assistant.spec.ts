import { expect, test } from '@playwright/test'
import { after, before } from './fake-model-script.mjs'

const api = { 'X-Refract': '1' }

// Ask the assistant for a change: its reasoning shows, the change arrives as a
// review, and accepting it writes the file.
test('the assistant proposes a change that is reviewed and accepted', async ({ page, request }) => {
  const doc = `\\documentclass{article}\n\\begin{document}\n${before}\n\\end{document}\n`
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Assistant test' } })).json()
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: doc })
  await request.patch(`/api/projects/${project.id}`, { headers: api, data: { main_file: 'main.tex' } })
  const provider = await request.put('/api/providers/test', {
    headers: api,
    data: { kind: 'openrouter', name: 'Test', api_key: 'test', base_url: 'http://127.0.0.1:18091/api/v1' },
  })
  expect(provider.ok()).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  await page.getByPlaceholder('Ask anything').fill('Make the greeting friendlier.')
  await page.keyboard.press('Enter')

  // The turn ends in a review; the floating chat then folds into its bar.
  await expect(page.getByRole('button', { name: 'Accept all' })).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: 'Open chat' }).click()
  await expect(page.getByRole('button', { name: 'Reasoning', exact: true })).toBeVisible()
  await expect(page.getByText('I changed the greeting.')).toBeVisible()
  // Nothing is written before the review is accepted.
  expect(await (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers: api })).text()).toBe(doc)

  await page.getByRole('button', { name: 'Accept all' }).click()
  const read = async () => (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers: api })).text()
  await expect.poll(read).toBe(doc.replace(before, after))
})
