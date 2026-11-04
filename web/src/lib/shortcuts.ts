import { t } from './i18n'

export const isMac = /Mac|iPhone|iPad/.test(navigator.platform)
export const mod = isMac ? '⌘' : 'Ctrl+'
export const holdKey = isMac ? '⌘' : 'Ctrl'

export const shortcutGroups: { title: string; items: [string, string][] }[] = [
  {
    title: t('General'),
    items: [
      [`${mod}K`, t('Command palette')],
      [t('Hold {key}', { key: holdKey }), t('Show these shortcuts')],
      ['?', t('Show these shortcuts')],
    ],
  },
  {
    title: t('Editor'),
    items: [
      [`${mod}↵`, t('Compile')],
      [`${mod}J`, t('Show the cursor position in the PDF')],
      [t('{mod}+click', { mod }), t('In the PDF: jump to the source')],
      [`${mod}S`, t('Save now (it also saves on its own)')],
      [`${mod}⇧M`, t('Toggle word count')],
      [`${mod}W`, t('Close the file (when installed as an app)')],
    ],
  },
  {
    title: t('Quick actions'),
    items: [
      [`${mod}K`, t('With text selected: edit or explain it')],
      ['Tab', t('Accept the suggestion')],
      ['Esc', t('Reject the suggestion')],
    ],
  },
  {
    title: t('Assistant'),
    items: [
      ['↵', t('Send')],
      ['⇧↵', t('New line')],
      ['Esc', t('Collapse the chat')],
    ],
  },
]

export const editorShortcuts = shortcutGroups[1].items
