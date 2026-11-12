import { useEffect, useState } from 'react'
import { api, errorText, type Model, type ModelUsage } from '@/api'
import { monthStart } from '@/lib/usage'

/**
 * The signed-in account's token usage since the start of this month, per
 * model, and the models on offer (for their names). It loads while active is
 * true, again each time it turns true, so an opened menu shows fresh numbers.
 */
export function useMonthUsage(active = true) {
  const [since] = useState(() => monthStart(new Date()))
  const [usage, setUsage] = useState<ModelUsage[] | null>(null)
  const [error, setError] = useState('')
  const [models, setModels] = useState<Model[]>([])

  useEffect(() => {
    if (!active) return
    let live = true
    api<{ models: ModelUsage[] }>(`/api/usage?since=${since}`).then(
      (r) => live && (setUsage(r.models), setError('')),
      (e) => live && setError(errorText(e)),
    )
    api<Model[]>('/api/models').then(
      (ms) => live && setModels(ms),
      () => {}, // names only; ids are the fallback
    )
    return () => {
      live = false
    }
  }, [active, since])

  return { since, usage, error, models }
}
