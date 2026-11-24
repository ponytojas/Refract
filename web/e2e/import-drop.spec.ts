import { expect, test, type Page } from '@playwright/test'

const api = { 'X-Refract': '1' }

// Playwright can't drag a real folder from Finder, so the drop event carries a DataTransfer whose
// items hand out FileSystemEntry look-alikes: the page's reading, upload and the server's import are real.
// A real DataTransfer with a File covers the zip path (its entry is a file, or null).
async function drop(page: Page, tree: Record<string, string> | null, zip?: { name: string; bytes: number[] }) {
  await page.evaluate(
    ({ tree, zip }) => {
      type Node = { isFile: boolean; isDirectory: boolean; name: string; [k: string]: unknown }
      const fileEntry = (name: string, text: string): Node => ({
        isFile: true,
        isDirectory: false,
        name,
        file: (ok: (f: File) => void) => ok(new File([text], name)),
      })
      const dirEntry = (name: string, kids: Node[]): Node => ({
        isFile: false,
        isDirectory: true,
        name,
        createReader: () => {
          let at = 0
          // Batches of one, as a reminder that readEntries must be called until it's empty.
          return { readEntries: (ok: (e: Node[]) => void) => setTimeout(() => ok(kids.slice(at, ++at))) }
        },
      })
      const build = (name: string, paths: [string, string][]): Node => {
        const files = paths.filter(([p]) => !p.includes('/')).map(([p, t]) => fileEntry(p, t))
        const subs = [...new Set(paths.filter(([p]) => p.includes('/')).map(([p]) => p.split('/')[0]))]
        const dirs = subs.map((s) =>
          build(
            s,
            paths.filter(([p]) => p.startsWith(s + '/')).map(([p, t]) => [p.slice(s.length + 1), t]),
          ),
        )
        return dirEntry(name, [...files, ...dirs])
      }
      const items: unknown[] = []
      if (tree) {
        const root = build('Dropped thesis', Object.entries(tree))
        items.push({ kind: 'file', type: '', getAsFile: () => null, webkitGetAsEntry: () => root })
      }
      const dt = new DataTransfer()
      if (zip) dt.items.add(new File([new Uint8Array(zip.bytes)], zip.name, { type: 'application/zip' }))
      for (const item of dt.items) items.push(item)
      const fake = { items, files: dt.files, types: ['Files'] }
      const event = new DragEvent('drop', { bubbles: true, cancelable: true })
      Object.defineProperty(event, 'dataTransfer', { value: tree ? fake : dt })
      document.querySelector('main')!.dispatchEvent(event)
    },
    { tree, zip },
  )
}

test('dropping a folder or a zip on the project list imports it', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.getByRole('main')).toBeVisible()

  await drop(page, {
    'main.tex': '\\documentclass{article}\n\\begin{document}\nDropped.\n\\end{document}\n',
    'figs/a.txt': 'a',
    'figs/deep/b.txt': 'b',
  })
  await expect(page.getByText('Imported Dropped thesis')).toBeVisible()
  const projects: { id: string; name: string }[] = await (await request.get('/api/projects', { headers: api })).json()
  const made = projects.find((p) => p.name === 'Dropped thesis')!
  expect(made).toBeTruthy()
  const files = await (await request.get(`/api/projects/${made.id}/files`, { headers: api })).text()
  for (const f of ['main.tex', 'figs/a.txt', 'figs/deep/b.txt']) expect(files).toContain(f)

  // The project's own export, dropped back as a zip, makes another project.
  const zip = await (await request.get(`/api/projects/${made.id}/export`, { headers: api })).body()
  await drop(page, null, { name: 'Zipped thesis.zip', bytes: [...zip] })
  await expect(page.getByText('Imported Zipped thesis')).toBeVisible()
})
