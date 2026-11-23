import { expect, test } from '@playwright/test'

const api = { 'X-Refract': '1' }

for (const lines of [3, 150]) {
  test(`scroll past the end of a ${lines}-line file with the assistant floating`, async ({ page, request }) => {
    const project = await (
      await request.post('/api/projects', { headers: api, data: { name: `Editor scroll ${lines}` } })
    ).json()
    const source = Array.from({ length: lines - 1 }, (_, index) => `% Line ${index + 1}`)
    if (lines > 3) {
      source[0] = '\\documentclass{article}'
      source[1] = '\\begin{document}'
      source[lines - 4] = '\\section{Near the end}'
      source[lines - 3] = '\\end{document}'
    }
    const doc = [...source, '% Last line'].join('\n')
    const file = await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: doc })
    expect(file.ok()).toBeTruthy()
    const updated = await request.patch(`/api/projects/${project.id}`, {
      headers: api,
      data: { main_file: 'main.tex' },
    })
    expect(updated.ok()).toBeTruthy()

    await page.route('**/api/chatgpt/status', (route) =>
      route.fulfill({
        json: {
          ai_ready: false,
          welcome_seen: true,
          status: { signed_in: false, plan_granted: false, limit_reached: false },
        },
      }),
    )
    await page.goto(`/p/${project.id}`)
    await page.getByRole('button', { name: 'main.tex' }).first().click()
    const notice = page.getByText('No AI connected.', { exact: false })
    await expect(notice).toBeVisible()

    const scroller = page.locator('.cm-scroller')
    const content = page.locator('.cm-content')
    const last = content.locator('.cm-line').filter({ hasText: /^% Last line$/ })
    const previous = content.locator('.cm-line').filter({ hasText: new RegExp(`^% Line ${lines - 1}$`) })

    async function scrollLastLineToTop() {
      await expect
        .poll(async () => {
          await scroller.evaluate((element) => (element.scrollTop = element.scrollHeight))
          return Math.abs((await last.boundingBox())!.y - (await scroller.boundingBox())!.y)
        })
        .toBeLessThan(2)
      const previousBox = await previous.boundingBox()
      if (previousBox)
        expect(previousBox.y + previousBox.height).toBeLessThanOrEqual((await scroller.boundingBox())!.y + 1)
    }

    if (lines > 3) {
      await page.getByPlaceholder('Ask anything').fill('First line\nSecond line\nThird line')
      await content.focus()
      await page.keyboard.press('ControlOrMeta+End')
      await expect
        .poll(async () => (await last.boundingBox())!.y + (await last.boundingBox())!.height)
        .toBeLessThan((await notice.boundingBox())!.y)

      await page.getByRole('button', { name: 'Minimize to bubble' }).click()
      const bubble = page.getByRole('button', { name: 'Open assistant' })
      await content.focus()
      await page.keyboard.press('ControlOrMeta+Home')
      await page.keyboard.press('ControlOrMeta+End')
      await expect
        .poll(async () => (await last.boundingBox())!.y + (await last.boundingBox())!.height)
        .toBeLessThan((await bubble.boundingBox())!.y)
      await bubble.click()
      const handle = page.getByRole('separator', { name: 'Resize assistant' })
      const chatTop = () => handle.evaluate((grip) => grip.parentElement!.getBoundingClientRect().top)
      // The sheet scales in when it opens: measure the grip once it has settled, or the drag can miss it.
      await handle.evaluate((grip) => Promise.all(grip.parentElement!.getAnimations().map((a) => a.finished)))
      const before = await chatTop()
      const grip = (await handle.boundingBox())!
      const editor = (await scroller.boundingBox())!
      await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2)
      await page.mouse.down()
      await page.mouse.move(grip.x + grip.width / 2, editor.y)
      await page.mouse.up()
      // The drag made the sheet as tall as it can be, short of the strip kept above it.
      await expect.poll(chatTop).toBeLessThan(before - 100)
      await expect.poll(async () => (await chatTop()) - editor.y).toBeGreaterThanOrEqual(48)

      // Line edges are fractional: flush with the sheet is within half a pixel.
      const lastBottomOverChat = async () => {
        const box = (await last.boundingBox())!
        return box.y + box.height - (await chatTop())
      }
      await content.focus()
      await page.keyboard.press('ControlOrMeta+Home')
      await page.keyboard.press('ControlOrMeta+End')
      await expect.poll(lastBottomOverChat).toBeLessThanOrEqual(0.5)

      await page.getByRole('button', { name: 'Near the end' }).click()
      const heading = content.locator('.cm-line').filter({ hasText: 'Near the end' })
      await expect
        .poll(async () => {
          const line = await heading.boundingBox()
          return !!line && line.y >= editor.y - 0.5 && line.y + line.height <= (await chatTop()) + 0.5
        })
        .toBe(true)

      await page.setViewportSize({ width: 1500, height: 700 })
      await expect.poll(async () => (await chatTop()) - (await scroller.boundingBox())!.y).toBeGreaterThanOrEqual(48)
      await page.getByRole('button', { name: 'Dock to the side' }).click()
      await expect(handle).toHaveCount(0)
    }
    await scrollLastLineToTop()
    await page.setViewportSize({ width: 1500, height: 630 })
    await scrollLastLineToTop()

    await last.getByText('% Last line', { exact: true }).click({ clickCount: 3 })
    await expect.poll(() => page.evaluate(() => getSelection()?.toString().trim())).toBe('% Last line')
    await page.keyboard.insertText('% Edited line')
    await expect(content.getByText('% Edited line', { exact: true })).toBeVisible()
  })
}
