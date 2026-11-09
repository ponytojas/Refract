// Where this Refract runs (REFRACT_MODE): a self-hosted install or the hosted
// service. Set once from /api/me before the app renders (main.tsx), so plain
// functions such as describe() can read it too.
export type Mode = 'selfhost' | 'saas'

let mode: Mode = 'selfhost'

export function setMode(m: unknown) {
  if (m === 'selfhost' || m === 'saas') mode = m
}

// The ChatGPT plan is self-hosted only; the hosted service offers API providers.
export function chatgptOffered(): boolean {
  return mode === 'selfhost'
}
