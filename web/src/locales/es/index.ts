import wordcounter from './wordcounter'
import app from './app'
import assistant from './assistant'
import ui from './ui'
import workspace from './workspace'

export default { ...ui, ...app, ...workspace, ...assistant, ...wordcounter }
