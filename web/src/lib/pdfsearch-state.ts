import { emptyPdfMatches, findPdfMatches, type PdfMatch, type PdfTextIndex } from './pdfsearch.ts'

export type PdfScrollRequest = { id: number; match: PdfMatch }
export type PdfSearchState = {
  open: boolean
  query: string
  current: number
  indexed: { doc: object; pages: PdfTextIndex[] } | null
  matches: PdfMatch[]
  failedDoc: object | null
  navigation: number
  request: { id: number; doc: object } | null
}
export const initialPdfSearch: PdfSearchState = {
  open: false,
  query: '',
  current: 0,
  indexed: null,
  matches: emptyPdfMatches,
  failedDoc: null,
  navigation: 0,
  request: null,
}
type Action =
  | { type: 'show'; doc: object }
  | { type: 'close' }
  | { type: 'query'; doc: object | null; query: string }
  | { type: 'select'; doc: object; index: number }
  | { type: 'loaded'; doc: object; pages: PdfTextIndex[] }
  | { type: 'failed'; doc: object }

// Keep selection and navigation separate: replacing a compiled document may
// clamp the selected index, but only an explicit user action requests a jump.
export function pdfSearchReducer(state: PdfSearchState, action: Action): PdfSearchState {
  switch (action.type) {
    case 'show':
      return {
        ...state,
        open: true,
        navigation: state.navigation + 1,
        request: { id: state.navigation + 1, doc: action.doc },
      }
    case 'close':
      return { ...state, open: false, failedDoc: null, request: null }
    case 'query': {
      const matches =
        state.indexed?.doc === action.doc ? findPdfMatches(state.indexed.pages, action.query) : emptyPdfMatches
      return {
        ...state,
        query: action.query,
        current: 0,
        matches,
        navigation: state.navigation + 1,
        request: action.doc ? { id: state.navigation + 1, doc: action.doc } : null,
      }
    }
    case 'select':
      return {
        ...state,
        current: action.index,
        navigation: state.navigation + 1,
        request: { id: state.navigation + 1, doc: action.doc },
      }
    case 'loaded': {
      const matches = findPdfMatches(action.pages, state.query)
      return {
        ...state,
        indexed: { doc: action.doc, pages: action.pages },
        matches,
        failedDoc: null,
        current: Math.min(state.current, Math.max(0, matches.length - 1)),
      }
    }
    case 'failed':
      return { ...state, failedDoc: action.doc }
  }
}

export function pdfSearchMatches(state: PdfSearchState, doc: object | null): PdfMatch[] {
  return state.open && state.indexed?.doc === doc ? state.matches : emptyPdfMatches
}

export function pdfScrollRequest(state: PdfSearchState, doc: object | null): PdfScrollRequest | undefined {
  const match = pdfSearchMatches(state, doc)[state.current]
  if (match && state.request?.doc === doc) return { id: state.request.id, match }
}
