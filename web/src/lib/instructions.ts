// REFRACT.md: instructions the assistant and quick actions always follow in a
// project. The server reads it for assistant turns; quick actions aren't tied
// to a project, so the editor sends it along.

export const INSTRUCTIONS_FILE = 'REFRACT.md'

// The server uses at most 2 KB of it in quick actions and rejects more than
// 16 KB; 4096 UTF-16 units stay under that whatever the text is.
const SEND_LIMIT = 4096

/** The starting file: only a comment, so until the author writes something the assistant gets nothing. */
export function instructionsTemplate(t: (text: string) => string): string {
  return [
    '<!--',
    t(
      'Instructions the assistant always follows in this project: journal style, spelling, notation, what it must not change.',
    ),
    t('Write them below as plain sentences. Text inside these comment marks is ignored.'),
    t('You can also ask the assistant to remember something: it proposes an edit to this file.'),
    '-->',
    '',
    '',
  ].join('\n')
}

/** What the server would use: the text without HTML comments, trimmed. */
export function instructionsText(text: string): string {
  return text.replace(/<!--[\s\S]*?(-->|$)/g, '').trim()
}

/** The part of REFRACT.md sent with a quick action, or undefined when it says nothing. */
export function forQuickAction(text: string | undefined): string | undefined {
  const body = text === undefined ? '' : instructionsText(text)
  return body ? body.slice(0, SEND_LIMIT) : undefined
}
