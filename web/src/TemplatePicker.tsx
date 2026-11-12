import { useEffect, useRef, useState } from 'react'
import { FileText, Loader2, Trash2 } from 'lucide-react'
import { api, errorText } from './api'
import { Button } from '@/components/ui/button'
import { t } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from '@/components/ui/alert-dialog'

export type ProjectTemplate = { id: string; name: string; builtin: boolean }

const starters: Record<string, { name: string; description: string }> = {
  article: { name: t('Article'), description: t('Abstract, methods, results and discussion.') },
  thesis: { name: t('Thesis'), description: t('Chapters, contents and an appendix.') },
  beamer: { name: t('Presentation'), description: t('Beamer slides with the Madrid theme.') },
  letter: { name: t('Letter'), description: t('Sender, recipient and a formal closing.') },
  cv: { name: t('CV'), description: t('Profile, education, experience and skills.') },
}

export default function TemplatePicker({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (id: string) => void
  disabled: boolean
}) {
  const [templates, setTemplates] = useState<ProjectTemplate[] | null>(null)
  const [error, setError] = useState('')
  const [loadVersion, setLoadVersion] = useState(0)
  const [deleteError, setDeleteError] = useState('')
  const [deleting, setDeleting] = useState<ProjectTemplate | null>(null)
  const [removing, setRemoving] = useState(false)
  const removePending = useRef(false)
  useEffect(() => {
    const controller = new AbortController()
    api<ProjectTemplate[]>('/api/templates', { signal: controller.signal })
      .then((items) => {
        if (!controller.signal.aborted) setTemplates(items)
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(errorText(e))
      })
    return () => controller.abort()
  }, [loadVersion])

  async function remove() {
    if (!deleting || removePending.current) return
    removePending.current = true
    setDeleteError('')
    setRemoving(true)
    try {
      await api(`/api/projects/${deleting.id}`, { method: 'DELETE' })
      setTemplates((items) => items?.filter((item) => item.id !== deleting.id) ?? null)
      if (value === deleting.id) onChange('')
      setDeleting(null)
    } catch (e) {
      setDeleteError(errorText(e))
    } finally {
      removePending.current = false
      setRemoving(false)
    }
  }

  function option(id: string, name: string, description: string) {
    return (
      <label
        className={cn(
          'flex min-h-20 w-full min-w-0 cursor-pointer items-start gap-2 rounded-lg border p-3 text-left transition-colors focus-within:ring-2 focus-within:ring-ring hover:bg-accent',
          disabled && 'pointer-events-none opacity-50',
          value === id && 'border-primary bg-accent',
        )}
      >
        <FileText className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{name}</span>
          <span className="mt-1 block text-xs text-muted-foreground">{description}</span>
        </span>
        <input
          type="radio"
          name="project-template"
          value={id}
          checked={value === id}
          aria-label={name}
          disabled={disabled}
          onChange={() => onChange(id)}
          className="mt-0.5 size-4 shrink-0 accent-primary"
        />
      </label>
    )
  }

  return (
    <div
      role="radiogroup"
      aria-label={t('Project template')}
      className="mt-4 min-h-0 min-w-0 space-y-3 overflow-y-auto p-0.5"
    >
      <p className="text-sm font-medium">{t('Choose a starting point')}</p>
      {error && (
        <div className="flex items-center gap-2">
          <p role="alert" className="flex-1 text-sm text-destructive">
            {error}
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => {
              setError('')
              setTemplates(null)
              setLoadVersion((n) => n + 1)
            }}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {templates === null && !error && (
        <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          {t('Loading templates…')}
        </p>
      )}
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {option('', t('Blank project'), t('Start with an empty project.'))}
        {templates
          ?.filter((item) => item.builtin)
          .map((item) => (
            <div key={item.id}>
              {option(item.id, starters[item.id]?.name ?? item.name, starters[item.id]?.description ?? '')}
            </div>
          ))}
      </div>
      {!!templates?.some((item) => !item.builtin) && (
        <>
          <p className="text-sm font-medium">{t('Your templates')}</p>
          <div className="space-y-2">
            {templates
              .filter((item) => !item.builtin)
              .map((item) => (
                <div key={item.id} className="flex items-center gap-1">
                  {option(item.id, item.name, t('Private copy of your saved project.'))}
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    disabled={disabled}
                    aria-label={t('Delete template {name}', { name: item.name })}
                    onClick={() => {
                      setDeleteError('')
                      setDeleting(item)
                    }}
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
          </div>
        </>
      )}
      <AlertDialog open={!!deleting} onOpenChange={(open) => !open && !removePending.current && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete template {name}?', { name: deleting?.name ?? '' })}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('Projects already created from this template are kept.')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && (
            <p role="alert" className="text-sm text-destructive">
              {deleteError}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removing}>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={removing}
              onClick={(e) => {
                e.preventDefault()
                remove()
              }}
            >
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
