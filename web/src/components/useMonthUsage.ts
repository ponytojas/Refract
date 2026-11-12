import { useCallback, useEffect, useState } from 'react'
import { api, errorText, type Allowance, type Model, type ModelUsage, type UsageReport } from '@/api'
import { monthStart } from '@/lib/usage'

/**
 * The signed-in account's token usage since the start of this month, per
 * model, its included-AI allowance if it has one, and the models on offer (for
 * their names). It loads while active is true, again each time it turns true
 * (an opened menu shows fresh numbers) and on retry(). The month is worked out
 * at each load, so a page left open across the 1st shows the new month.
 *
 * When a load fails, usage is null and error says why: numbers from an
 * earlier load are never shown as current.
 */
export function useMonthUsage(active = true) {
  const [since, setSince] = useState(() => monthStart(new Date()))
  const [usage, setUsage] = useState<ModelUsage[] | null>(null)
  const [allowance, setAllowance] = useState<Allowance | undefined>()
  const [error, setError] = useState('')
  const [models, setModels] = useState<Model[]>([])
  const [attempt, setAttempt] = useState(0)
  const retry = useCallback(() => setAttempt((n) => n + 1), [])

  useEffect(() => {
    if (!active) return
    const req = { live: true }
    const from = monthStart(new Date())
    api<UsageReport>(`/api/usage?since=${from}`).then(
      (r) => {
        if (!req.live) return
        setSince(from)
        setUsage(r.models)
        setAllowance(r.allowance)
        setError('')
      },
      (e) => {
        if (!req.live) return
        setUsage(null)
        setAllowance(undefined)
        setError(errorText(e))
      },
    )
    api<Model[]>('/api/models').then(
      (ms) => req.live && setModels(ms),
      () => {}, // names only; ids are the fallback
    )
    return () => {
      req.live = false
    }
  }, [active, attempt])

  return { since, usage, allowance, error, retry, models }
}
