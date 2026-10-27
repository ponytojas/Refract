import { useCallback, useEffect, useState } from 'react'
import { api, type ChatGPTStatus } from './api'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import Projects from './Projects'
import SettingsDialog from './Settings'
import { openSettings } from '@/lib/prefs'
import Welcome from './Welcome'
import CommandPalette from './CommandPalette'
import ShortcutsOverlay from './ShortcutsOverlay'
import { getOverlay, setOverlay, useCommands } from '@/lib/commands'
import { setTheme } from '@/lib/theme'
import { t } from '@/lib/i18n'
import { FolderOpen, Keyboard, Monitor, Moon, Settings, Sun } from 'lucide-react'
import Workspace from './Workspace'
import { navigate, usePath } from '@/lib/router'

export default function App() {
  const [status, setStatus] = useState<ChatGPTStatus | null>(null)
  const refresh = useCallback(() => {
    api<ChatGPTStatus>('/api/chatgpt/status')
      .then(setStatus)
      .catch(() => {})
  }, [])
  useEffect(refresh, [refresh])
  // Old /settings links open the dialog over the project list.
  useEffect(() => {
    if (window.location.pathname === '/settings') {
      navigate('/', { replace: true })
      openSettings('assistant')
    }
  }, [])

  // ⌘K anywhere, also from inputs and the editor.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setOverlay(getOverlay() === 'palette' ? null : 'palette')
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [])

  const path = usePath()
  useCommands(() => [
    ...(path !== '/' ? [{ id: 'nav:projects', group: 'Navigate', label: t('All projects'), icon: FolderOpen, run: () => navigate('/') }] : []),
    { id: 'app:settings', group: 'General', label: t('Settings'), icon: Settings, keywords: 'preferences account chatgpt', run: () => openSettings() },
    { id: 'app:shortcuts', group: 'General', label: t('Keyboard shortcuts'), icon: Keyboard, shortcut: '?', run: () => setOverlay('shortcuts') },
    { id: 'theme:light', group: 'General', label: t('Light theme'), icon: Sun, keywords: 'appearance', run: () => setTheme('light') },
    { id: 'theme:dark', group: 'General', label: t('Dark theme'), icon: Moon, keywords: 'appearance', run: () => setTheme('dark') },
    { id: 'theme:system', group: 'General', label: t('System theme'), icon: Monitor, keywords: 'appearance', run: () => setTheme('system') },
  ])
  const welcome = !!status && status.status.signed_in && status.status.plan_granted && !status.welcome_seen

  return (
    // After the first tooltip opens, neighbours open instantly (within the provider's 400 ms timeout).
    <TooltipProvider delay={500}>
      {path.startsWith('/p/') ? (
        // Keyed: each project gets a fresh workspace (its autosave writes to its own id).
        <Workspace key={path} id={path.slice(3)} status={status} />
      ) : (
        <Projects status={status} />
      )}
      {status && <Welcome open={welcome} manageUsage={status.manage_usage} onClose={() => setStatus({ ...status, welcome_seen: true })} />}
      <SettingsDialog status={status} onChange={setStatus} />
      <CommandPalette />
      <ShortcutsOverlay />
      <Toaster position="bottom-right" />
    </TooltipProvider>
  )
}
