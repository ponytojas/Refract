import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import Login from './Login'
import './index.css'

const root = createRoot(document.getElementById('root')!)
// The app downloads while the session is checked; signed out
// (REFRACT_AUTH=password), the API answers 401 and only the small sign-in page shows.
const app = import('./App')
fetch('/api/me', { headers: { 'X-Refract': '1' } })
  .then((r) => r.status === 401, () => false)
  .then(async (signedOut) => {
    const App = signedOut ? null : (await app).default
    root.render(<StrictMode>{App ? <App /> : <Login />}</StrictMode>)
  })
