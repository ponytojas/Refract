import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import Login from './Login'
import { setMode } from './lib/mode'
import { lang } from './lib/i18n'
import './index.css'

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
      setMode(
        await r.json().then(
          (me) => me?.mode,
          () => undefined,
        ),
      )
      return false
    },
    () => false,
  )
  .then(async (signedOut) => {
    const App = signedOut || accountPage ? null : (await app).default
    root.render(<StrictMode>{App ? <App /> : <Login signedIn={!signedOut} />}</StrictMode>)
  })
