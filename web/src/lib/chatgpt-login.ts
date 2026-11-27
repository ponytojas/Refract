// The in-server ChatGPT sign-in (GET /api/chatgpt/login), as Settings shows it.

export type LoginState = {
  state: 'idle' | 'pending' | 'done' | 'failed'
  kind?: 'cancelled' | 'timeout' | 'port_busy' | 'other_account' | 'other'
  message?: string
}

// failureText explains a failed sign-in in plain words; t is the translator
// (t), passed in so this stays testable without the locale.
export function failureText(st: LoginState, t: (s: string) => string): string {
  switch (st.kind) {
    case 'cancelled':
      return t('Sign-in was cancelled.')
    case 'timeout':
      return t('Sign-in took too long and stopped. Try again when you are ready.')
    case 'port_busy':
      return t('Another app is signing in to ChatGPT on this computer (port 1455). Close it and try again.')
    case 'other_account':
      return t('You signed in with a different ChatGPT account than the one connected before.')
    default:
      return st.message || t('Sign-in did not complete.')
  }
}

// settled is whether polling can stop.
export function settled(st: LoginState): boolean {
  return st.state !== 'pending'
}
