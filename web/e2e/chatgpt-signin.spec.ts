import { expect, test, type Page } from '@playwright/test'

const headers = { 'X-Refract': '1' }

async function openAssistantSettings(page: Page) {
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: /^Assistant/ }).click()
}

// The sign-in finishes in the browser while Settings is closed: reopening it
// shows the connected account without a reload.
test('reopening Settings after a sign-in finished elsewhere shows the account', async ({ page, request }) => {
  let signedIn = false
  let login = { state: 'pending' }
  await page.addInitScript(() => {
    if (!localStorage.getItem('refract-prefs'))
      localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' }))
  })
  await page.route('**/api/chatgpt/status', (route) =>
    route.fulfill({
      json: {
        ai_ready: signedIn,
        welcome_seen: true,
        sign_in_here: true,
        manage_usage: 'https://chatgpt.com',
        default_model: '',
        status: {
          provider: 'chatgpt',
          signed_in: signedIn,
          plan_granted: signedIn,
          email: signedIn ? 'ana@example.com' : undefined,
          limit_reached: false,
        },
      },
    }),
  )
  await page.route('**/api/chatgpt/login', (route) => route.fulfill({ json: login }))

  const project = await (await request.post('/api/projects', { headers, data: { name: 'Sign in' } })).json()
  await page.goto(`/p/${project.id}`)
  await openAssistantSettings(page)
  await expect(page.getByText('Waiting for ChatGPT…')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)

  signedIn = true
  login = { state: 'done' }
  await openAssistantSettings(page)
  await expect(page.getByRole('dialog').getByText('ana@example.com')).toBeVisible()
  await expect(page.getByRole('dialog').getByText('Not connected')).toHaveCount(0)
})
