// The macOS app has no title bar: the page reaches the top edge and the bars draw where the
// title bar would be. WKWebView has no `-webkit-app-region`, so a mousedown on a bar's empty
// part (marked `data-window-drag`) is forwarded to the app, which moves the window and
// handles a double click as the system setting says. In a browser, or anywhere else
// without that handler, nothing is installed.

type Handler = { postMessage: (message: unknown) => void }

declare global {
  interface Window {
    webkit?: { messageHandlers?: { refractWindow?: Handler } }
  }
}

/** What is clicked in a bar and must keep working instead of dragging. */
export const interactive =
  'button, a, input, textarea, select, summary, label, [role=button], [role=tab], [role=menuitem], [contenteditable], [data-no-drag]'

/** True when a mousedown on `target` should move the window. */
export function dragStarts(target: EventTarget | null, button: number): boolean {
  if (button !== 0 || !(target instanceof Element)) return false
  return !!target.closest('[data-window-drag]') && !target.closest(interactive)
}

/** Installs the listener; returns whether it did (only inside the macOS app). */
export function installWindowDrag(doc: Document = document, win: Window = window): boolean {
  const handler = win.webkit?.messageHandlers?.refractWindow
  if (!handler) return false
  doc.addEventListener('mousedown', (e) => {
    if (e.defaultPrevented || !dragStarts(e.target, e.button)) return
    handler.postMessage({ type: 'drag' })
  })
  return true
}
