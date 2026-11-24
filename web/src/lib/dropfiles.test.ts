import assert from 'node:assert/strict'
import { test } from 'node:test'
import { maxEntries, readDropped } from './dropfiles.ts'

const file = (name: string) => ({
  isFile: true,
  isDirectory: false,
  name,
  file: (ok: (f: File) => void) => ok(new File(['x'], name)),
})
const dir = (name: string, kids: unknown[]) => {
  let given = false
  return {
    isFile: false,
    isDirectory: true,
    name,
    // two calls: the children, then an empty batch
    createReader: () => ({
      readEntries: (ok: (e: unknown[]) => void) => {
        ok(given ? [] : kids)
        given = true
      },
    }),
  }
}

test('folders are walked with relative paths, files pass through', async () => {
  const zip = new File(['z'], 'a.zip')
  const tree = dir('thesis', [file('main.tex'), dir('figs', [file('a.png')])])
  const got = await readDropped([
    { kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => tree as never },
    { kind: 'file', getAsFile: () => zip, webkitGetAsEntry: () => file('a.zip') as never },
    { kind: 'string', getAsFile: () => null },
  ])
  assert.deepEqual(got.files, [zip])
  assert.equal(got.folders[0].name, 'thesis')
  assert.deepEqual(
    got.folders[0].files.map((f) => f.path),
    ['thesis/main.tex', 'thesis/figs/a.png'],
  )
})

test('directories are read until an empty batch, in batches of 100', async () => {
  const kids = Array.from({ length: 250 }, (_, i) => file(`f${i}.tex`))
  let at = 0
  const big = {
    isFile: false,
    isDirectory: true,
    name: 'big',
    createReader: () => ({
      readEntries: (ok: (e: unknown[]) => void) => {
        ok(kids.slice(at, at + 100))
        at += 100
      },
    }),
  }
  const got = await readDropped([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => big as never }])
  assert.equal(got.folders[0].files.length, 250)
})

test('a folder that contains itself stops at the depth guard instead of looping', async () => {
  const loop: Record<string, unknown> = { isFile: false, isDirectory: true, name: 'loop' }
  loop.createReader = () => {
    let given = false
    return {
      readEntries: (ok: (e: unknown[]) => void) => {
        ok(given ? [] : [loop])
        given = true
      },
    }
  }
  const got = await readDropped([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => loop as never }])
  assert.deepEqual(got.folders, [{ name: 'loop', files: [], tooDeep: true }])
  assert.ok(maxEntries > 5000)
})

test('a folder nested too deep is reported as such, not as too many files', async () => {
  let node: unknown = file('x.tex')
  for (let i = 0; i < 70; i++) node = dir(`d${i}`, [node])
  const got = await readDropped([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => node as never }])
  assert.equal(got.folders[0].tooDeep, true)
  assert.equal(got.folders[0].tooBig, undefined)
})

test('a read error is recorded for that folder and the others still load', async () => {
  const bad = {
    isFile: false,
    isDirectory: true,
    name: 'bad',
    createReader: () => ({ readEntries: (_ok: unknown, fail: (e: unknown) => void) => fail(new Error('denied')) }),
  }
  const good = dir('good', [file('a.tex')])
  const got = await readDropped([
    { kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => bad as never },
    { kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => good as never },
  ])
  assert.equal(got.folders[0].name, 'bad')
  assert.equal(got.folders[0].error, 'denied')
  assert.deepEqual(
    got.folders[1].files.map((f) => f.path),
    ['good/a.tex'],
  )
})

test('a folder with more entries than the limit is reported as too big', async () => {
  const kids = Array.from({ length: maxEntries + 1 }, (_, i) => file(`f${i}`))
  const got = await readDropped([
    { kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => dir('wide', kids) as never },
  ])
  assert.deepEqual(got.folders, [{ name: 'wide', files: [], tooBig: true }])
})

test('reading a folder stops once the entry limit is passed', async () => {
  let calls = 0
  const wide = {
    isFile: false,
    isDirectory: true,
    name: 'wide',
    createReader: () => ({
      readEntries: (ok: (e: unknown[]) => void) => {
        calls++
        ok(Array.from({ length: 1000 }, (_, i) => file(`f${calls}-${i}`)))
      },
    }),
  }
  const got = await readDropped([{ kind: 'file', getAsFile: () => null, webkitGetAsEntry: () => wide as never }])
  assert.equal(got.folders[0].tooBig, true)
  assert.equal(calls, maxEntries / 1000)
})
