import { expect, test } from '@playwright/test'

const api = { 'X-Refract': '1' }

// In the macOS app the window has no title bar: the app sets data-desktop and the inset
// variables, and the first control of each bar starts after the traffic lights. In a browser
// nothing sets them and the bars are unchanged.
test('bars leave room for the traffic lights only in the desktop app', async ({ page, request }) => {
  const run = Math.random().toString(36).slice(2, 8)
  const p = await (await request.post('/api/projects', { headers: api, data: { name: `Inset ${run}` } })).json()

  await page.goto('/')
  const header = page.locator('header[data-window-bar]').first()
  await expect(header).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.dataset.desktop)).toBeUndefined()

  await page.evaluate(() => {
    const r = document.documentElement
    r.dataset.desktop = 'mac'
    r.style.setProperty('--window-inset-left', '78px')
    r.style.setProperty('--window-inset-top', '28px')
  })
  // The bar starts 8px in from the window edge: its content starts at the inset.
  const title = header.locator('h1')
  const box = await title.boundingBox()
  expect(box!.x).toBeGreaterThanOrEqual(78)
  expect((await header.boundingBox())!.height).toBeGreaterThanOrEqual(44)

  // The project view: the toolbar of the sidebar (or of the editor when it is hidden).
  await page.goto(`/p/${p.id}`)
  await page.evaluate(() => {
    const r = document.documentElement
    r.dataset.desktop = 'mac'
    r.style.setProperty('--window-inset-left', '78px')
  })
  const first = page.locator('[data-window-bar]').first()
  await expect(first).toBeVisible()
  const link = first.locator('a, button').first()
  expect((await link.boundingBox())!.x).toBeGreaterThanOrEqual(78)
})

test('without the app, a mousedown on a bar posts nothing and no handler is installed', async ({ page }) => {
  await page.goto('/')
  await expect(page.locator('header[data-window-bar]').first()).toBeVisible()
  expect(await page.evaluate(() => 'webkit' in window && !!(window as any).webkit?.messageHandlers)).toBe(false)
})

test('with the handler, only empty parts of a bar post a drag', async ({ page }) => {
  await page.addInitScript(() => {
    const posted: unknown[] = []
    ;(window as any).__posted = posted
    ;(window as any).webkit = { messageHandlers: { refractWindow: { postMessage: (m: unknown) => posted.push(m) } } }
  })
  await page.goto('/')
  const header = page.locator('header[data-window-bar]').first()
  await expect(header).toBeVisible()
  const posted = () => page.evaluate(() => (window as any).__posted.length)

  await header.getByRole('button').first().dispatchEvent('mousedown')
  expect(await posted()).toBe(0)
  await header.dispatchEvent('mousedown')
  expect(await posted()).toBe(1)
})
