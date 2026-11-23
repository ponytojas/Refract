import { useEffect } from 'react'
import { toast } from 'sonner'
import { t } from '@/lib/i18n'
import { getMissingTools, type ToolName } from '@/lib/tools'

export const effects: Record<ToolName, () => string> = {
  git: () =>
    t(
      'Git is required to create projects and use project history. Ask whoever installed Refract to repair the installation or install Git.',
    ),
  tectonic: () => t("Tectonic is missing, so documents can't be compiled."),
  synctex: () => t('SyncTeX is missing, so jumping between the source and the PDF is unavailable.'),
  rg: () => t("ripgrep is missing, so the assistant can't search the project."),
}

let shown = false

/**
 * One notice, shown once per page load, on whichever page opens first, when the server started
 * without some of its tools, instead of failing on first use. Features that need a missing tool
 * also say so when used (see Workspace).
 */
export default function ToolsNotice() {
  useEffect(() => {
    const missing = getMissingTools()
    if (shown || missing.length === 0) return
    shown = true
    toast.warning(t('Some tools are missing'), {
      id: 'missing-tools',
      duration: 15000,
      closeButton: true,
      description: (
        <ul className="list-disc pl-4">
          {missing.map((n) => (
            <li key={n}>{effects[n]()}</li>
          ))}
        </ul>
      ),
    })
  }, [])
  return null
}
