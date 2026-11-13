import wordcounter from './wordcounter'
import pdf from './pdf'
import app from './app'
import completion from './completion'
import assistant from './assistant'
import ui from './ui'
import workspace from './workspace'
import templates from './templates'

export default { ...ui, ...app, ...workspace, ...assistant, ...wordcounter, ...pdf, ...completion, ...templates }
