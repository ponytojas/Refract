import { useEffect, useState } from 'react'
import { api, errorText, type Allowance, type Model, type ModelUsage, type UsageReport } from '@/api'
import { monthStart } from '@/lib/usage'

/**
 * The signed-in account's token usage since the start of this month, per
 * model, its included-AI allowance if it has one, and the models on offer (for
 * their names). It loads while active is true, again each time it turns true,
 * so an opened menu shows fresh numbers.
 */
export function useMonthUsage(active = true) {
  const [since] = useState(() => monthStart(new Date()))
  const [usage, setUsage] = useState<ModelUsage[] | null>(null)
  const [allowance, setAllowance] = useState<Allowance | undefined>()
  const [error, setError] = useState('')
  const [models, setModels] = useState<Model[]>([])

  useEffect(() => {
    if (!active) return
    let live = true
    api<UsageReport>(`/api/usage?since=${since}`).then(
      (r) => live && (setUsage(r.models), setAllowance(r.allowance), setError('')),
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

  return { since, usage, allowance, error, models }
}
