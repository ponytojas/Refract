// copyWhenReady puts text that is still on its way on the clipboard. Call it
// synchronously inside the click handler: the clipboard takes a promise of the
// text, so the write still counts as the person's gesture when the text
// arrives after a request (Safari and the desktop app's web view refuse a
// write made after an await). Resolves to whether the text was copied.
export function copyWhenReady(text: Promise<string>): Promise<boolean> {
  const later = () =>
    text
      .then((s) => navigator.clipboard.writeText(s))
      .then(
        () => true,
        () => false,
      )
  try {
    if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
      const blob = text.then((s) => new Blob([s], { type: 'text/plain' }))
      return navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]).then(() => true, later)
    }
  } catch {
    // No clipboard API here: try the plain write.
  }
  return later()
}
