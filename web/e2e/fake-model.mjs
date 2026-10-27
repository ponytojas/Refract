// A scripted OpenAI-compatible model for the browser tests (no network, no
// keys): it reasons, asks to edit main.tex, then answers. Started by
// playwright.config.ts; Refract reaches it as an OpenRouter-style provider.
import { createServer } from 'node:http'
import { after, before } from './fake-model-script.mjs'

const models = [{ id: 'test/thinker', name: 'Test Thinker', supported_parameters: ['tools', 'reasoning'] }]

const chunk = (delta, finish = null) => `data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`

createServer(async (req, res) => {
  if (req.method === 'GET') {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    return res.end(JSON.stringify({ data: models }))
  }
  let body = ''
  for await (const part of req) body += part
  const { messages, tools } = JSON.parse(body)
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  if (tools && !messages.some((m) => m.role === 'tool')) {
    res.write(chunk({ reasoning: 'The greeting should change, so I will edit main.tex.' }))
    const args = JSON.stringify({ path: 'main.tex', old_text: before, new_text: after })
    res.write(chunk({ tool_calls: [{ index: 0, id: 'call_1', type: 'function', function: { name: 'edit_file', arguments: args } }] }, 'tool_calls'))
  } else {
    res.write(chunk({ content: 'I changed the greeting.' }, 'stop'))
  }
  res.end('data: [DONE]\n\n')
}).listen(18091, '127.0.0.1')
