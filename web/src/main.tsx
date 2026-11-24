import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import Login from './Login'
import { setMode } from './lib/mode'
import { setMissingTools } from './lib/tools'
import { lang } from './lib/i18n'
import { flushPending } from './lib/flush'
import { installWindowDrag } from './lib/windowDrag'
import './index.css'

declare global {
  interface Window {
    /** For the desktop app before it quits: saves pending edits; false when some couldn't be saved. */
    refractFlush?: () => Promise<boolean>
  }
}
window.refractFlush = () => flushPending()
installWindowDrag()

const root = createRoot(document.getElementById('root')!)
// The app downloads while the session is checked; signed out
// (REFRACT_AUTH=builtin), the API answers 401 and only the small sign-in page shows.
// The links in emails (/verify, /reset) always show the account screens, signed in or not.
const accountPage = ['/verify', '/reset'].includes(location.pathname)
const app = import('./App')
fetch('/api/me', { headers: { 'X-Refract': '1', 'X-Refract-Lang': lang } })
  .then(
    async (r) => {
      if (r.status === 401) return true
      const me = await r.json().catch(() => undefined)
      setMode(me?.mode)
      setMissingTools(me?.missing_tools)
      return false
    },
    () => false,
  )
  .then(async (signedOut) => {
    const App = signedOut || accountPage ? null : (await app).default
    root.render(<StrictMode>{App ? <App /> : <Login signedIn={!signedOut} />}</StrictMode>)
  })
