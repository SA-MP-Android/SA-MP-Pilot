import assert from 'node:assert/strict'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { once } from 'node:events'
import { spawn } from 'node:child_process'
import test from 'node:test'

const sdkURL = pathToFileURL(fileURLToPath(new URL('./sdk.mjs', import.meta.url))).href

function startFixture() {
  const source = `
import { EVENT_CLIENT_CHAT, log, on, start } from ${JSON.stringify(sdkURL)}
on(EVENT_CLIENT_CHAT, async (event, instance) => {
  log('info', event.data.text)
  await instance.sendChat('hello')
})
await start()
`
  const child = spawn(process.execPath, ['--input-type=module', '--eval', source], {
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  child.stdout.setEncoding('utf8')
  let buffer = ''
  const messages = []
  const pending = []
  child.stdout.on('data', (chunk) => {
    buffer += chunk
    for (;;) {
      const newline = buffer.indexOf('\n')
      if (newline < 0) break
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (!line.trim()) continue
      const message = JSON.parse(line)
      const waiter = pending.shift()
      if (waiter) waiter(message)
      else messages.push(message)
    }
  })
  child.on('exit', () => {
    while (pending.length) pending.shift()?.(new Error('SDK fixture exited'))
  })
  child.nextMessage = () =>
    new Promise((resolve, reject) => {
      if (messages.length) {
        resolve(messages.shift())
        return
      }
      const timer = setTimeout(() => reject(new Error('timed out waiting for SDK message')), 2_000)
      pending.push((message) => {
        clearTimeout(timer)
        if (message instanceof Error) reject(message)
        else resolve(message)
      })
    })
  return child
}

test('SDK exposes the canonical event contract and handles API responses', async () => {
  const child = startFixture()
  try {
    const ready = await child.nextMessage()
    assert.equal(ready.type, 'ready')
    assert.deepEqual(ready.events, ['client.chat'])

    child.stdin.write(`${JSON.stringify({
      type: 'event',
      name: 'client.chat',
      instanceId: 'instance-1',
      time: '2026-01-01T00:00:00.000Z',
      data: { text: 'hello from server' },
    })}\n`)
    const log = await child.nextMessage()
    assert.equal(log.type, 'log')
    assert.equal(log.message, 'hello from server')

    const call = await child.nextMessage()
    assert.equal(call.type, 'call')
    assert.equal(call.method, 'instance.sendChat')
    assert.equal(call.instanceId, 'instance-1')
    assert.deepEqual(call.params, { text: 'hello' })
    child.stdin.write(`${JSON.stringify({ type: 'result', id: call.id })}\n`)

    child.stdin.write(`${JSON.stringify({ type: 'debug', id: 99, instanceId: 'instance-1', code: 'return 42' })}\n`)
    const debug = await child.nextMessage()
    assert.deepEqual(debug, { type: 'debug.result', id: 99, result: 42 })
  } finally {
    child.stdin.end()
    await once(child, 'exit')
  }
})

test('SDK rejects non-serializable API parameters immediately', async () => {
  const source = `
import { api } from ${JSON.stringify(sdkURL)}
try { await api('instance.sendChat', 'instance-1', { value: 1n }) } catch (error) { process.stdout.write(error.message) }
`
  const child = spawn(process.execPath, ['--input-type=module', '--eval', source], {
    stdio: ['ignore', 'pipe', 'ignore'],
  })
  let output = ''
  child.stdout.setEncoding('utf8')
  child.stdout.on('data', (chunk) => { output += chunk })
  await once(child, 'exit')
  assert.match(output, /not JSON serializable/)
})

test('SDK isolates synchronous handler errors', async () => {
  const source = `
import { dispatch, on } from ${JSON.stringify(sdkURL)}
on('test.event', () => { throw new Error('sync boom') })
await dispatch({ name: 'test.event' })
process.stderr.write('dispatch-completed')
`
  const child = spawn(process.execPath, ['--input-type=module', '--eval', source], {
    stdio: ['ignore', 'ignore', 'pipe'],
  })
  let stderr = ''
  child.stderr.setEncoding('utf8')
  child.stderr.on('data', (chunk) => { stderr += chunk })
  const [exitCode] = await once(child, 'exit')
  assert.equal(exitCode, 0)
  assert.match(stderr, /dispatch-completed/)
})

test('SDK truncates logs at a valid UTF-8 byte boundary', async () => {
  const source = `
import { log, MAX_LOG_MESSAGE_BYTES } from ${JSON.stringify(sdkURL)}
log('info', 'a'.repeat(MAX_LOG_MESSAGE_BYTES - 2) + '😀')
`
  const child = spawn(process.execPath, ['--input-type=module', '--eval', source], {
    stdio: ['ignore', 'pipe', 'ignore'],
  })
  let output = ''
  child.stdout.setEncoding('utf8')
  child.stdout.on('data', (chunk) => { output += chunk })
  const [exitCode] = await once(child, 'exit')
  assert.equal(exitCode, 0)
  const message = JSON.parse(output)
  assert.equal(message.type, 'log')
  assert.ok(Buffer.byteLength(message.message, 'utf8') <= 16 * 1024)
  assert.equal(message.message.endsWith('�'), false)
})
