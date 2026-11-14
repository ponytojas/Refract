/** The token in an emailed link's fragment (`#token=…`), or null. It stays in the fragment so it never reaches server logs or Referer. */
export function linkToken(hash: string): string | null {
  const token = new URLSearchParams(hash.replace(/^#/, '')).get('token')
  return token ? token : null
}
