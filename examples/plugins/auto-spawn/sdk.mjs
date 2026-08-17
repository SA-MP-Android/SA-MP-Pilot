import readline from 'node:readline'
import vm from 'node:vm'

const MESSAGE_READY = 'ready'
const MESSAGE_EVENT = 'event'
const MESSAGE_CALL = 'call'
const MESSAGE_RESULT = 'result'
const MESSAGE_LOG = 'log'
const MESSAGE_DEBUG = 'debug'
const MESSAGE_DEBUG_RESULT = 'debug.result'
const MESSAGE_SUBSCRIBE = 'subscribe'
const PROTOCOL_VERSION = 1
const LOG_LEVEL_INFO = 'info'
const LOG_LEVEL_WARN = 'warn'
const LOG_LEVEL_ERROR = 'error'
const API_REQUEST_TIMEOUT_MS = 15_000
const DEBUG_SYNC_TIMEOUT_MS = 5_000
const DEBUG_ASYNC_TIMEOUT_MS = 15_000
const MAX_PROTOCOL_MESSAGE_BYTES = 1 << 20
const METHOD = {
  getSnapshot: 'instance.getSnapshot',
  getChat: 'instance.getChat',
  sendChat: 'instance.sendChat',
  sendCommand: 'instance.sendCommand',
  requestSpawn: 'instance.requestSpawn',
  refreshScores: 'instance.refreshScores',
  setKeys: 'instance.setKeys',
  setAFK: 'instance.setAFK',
  teleport: 'instance.teleport',
  enterVehicle: 'instance.enterVehicle',
  exitVehicle: 'instance.exitVehicle',
  respondDialog: 'instance.respondDialog',
  clickPlayer: 'instance.clickPlayer',
  clickTextDraw: 'instance.clickTextDraw',
  action: 'instance.action',
}

export const EVENT_APPEARANCE = 'client.appearance'
export const EVENT_CHAT = 'client.chat'

const handlers = []
const pending = new Map()
const debugState = Object.create(null)
let nextId = 0

function write(message) {
  let encoded
  try {
    encoded = JSON.stringify(message)
    if (Buffer.byteLength(encoded ?? '', 'utf8') > MAX_PROTOCOL_MESSAGE_BYTES) {
      throw new Error('plugin protocol message is too large')
    }
  } catch (error) {
    encoded = JSON.stringify({
      type: MESSAGE_LOG,
      level: LOG_LEVEL_ERROR,
      message: `failed to encode plugin message: ${error.message}`,
    })
  }
  process.stdout.write(`${encoded}\n`)
}

export function log(level, message) {
  write({ type: MESSAGE_LOG, level, message: String(message) })
}

function displayValue(value) {
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

const originalConsole = globalThis.console
globalThis.console = {
  ...originalConsole,
  log: (...values) => log(LOG_LEVEL_INFO, values.map(displayValue).join(' ')),
  info: (...values) => log(LOG_LEVEL_INFO, values.map(displayValue).join(' ')),
  warn: (...values) => log(LOG_LEVEL_WARN, values.map(displayValue).join(' ')),
  error: (...values) => log(LOG_LEVEL_ERROR, values.map(displayValue).join(' ')),
}

export function api(method, instanceId, params = {}) {
  const id = ++nextId
  write({ type: MESSAGE_CALL, id, method, instanceId, params })
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id)
      reject(new Error(`plugin API request timed out after ${API_REQUEST_TIMEOUT_MS}ms`))
    }, API_REQUEST_TIMEOUT_MS)
    pending.set(id, { resolve, reject, timer })
  })
}

function matches(pattern, name) {
  return pattern === '*' || pattern === name || (pattern.endsWith('*') && name.startsWith(pattern.slice(0, -1)))
}

export function on(pattern, handler) {
  const subscription = { pattern, handler }
  handlers.push(subscription)
  write({ type: MESSAGE_SUBSCRIBE, events: handlers.map((item) => item.pattern) })
  return () => {
    const index = handlers.indexOf(subscription)
    if (index < 0) return
    handlers.splice(index, 1)
    write({ type: MESSAGE_SUBSCRIBE, events: handlers.map((item) => item.pattern) })
  }
}

export async function dispatch(event) {
  const normalized = {
    name: event.name,
    instanceId: event.instanceId ?? '',
    time: event.time ?? new Date().toISOString(),
    data: event.data,
  }
  await Promise.all(
    handlers
      .filter((handler) => matches(handler.pattern, normalized.name))
      .map((handler) =>
        Promise.resolve(handler.handler(normalized, instanceApi(normalized.instanceId))).catch((error) =>
          log('error', `${normalized.name}: ${error.stack ?? error.message ?? error}`),
        ),
      ),
  )
}

function withTimeout(promise, timeoutMs, message) {
  let timer
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), timeoutMs)
  })
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer))
}

export function instanceApi(instanceId) {
  return {
    call: (method, params = {}) => api(method, instanceId, params),
    getSnapshot: () => api(METHOD.getSnapshot, instanceId),
    getChat: (params = {}) => api(METHOD.getChat, instanceId, params),
    sendChat: (text) => api(METHOD.sendChat, instanceId, { text }),
    sendCommand: (command) => api(METHOD.sendCommand, instanceId, { command }),
    requestSpawn: () => api(METHOD.requestSpawn, instanceId),
    refreshScores: () => api(METHOD.refreshScores, instanceId),
    setKeys: (mask) => api(METHOD.setKeys, instanceId, { mask }),
    setAFK: (enabled) => api(METHOD.setAFK, instanceId, { enabled }),
    teleport: (x, y, z) => api(METHOD.teleport, instanceId, { x, y, z }),
    enterVehicle: (vehicleId, passenger = false) => api(METHOD.enterVehicle, instanceId, { vehicleId, passenger }),
    exitVehicle: () => api(METHOD.exitVehicle, instanceId),
    respondDialog: (dialogId, buttonId, listItem = 0, inputText = '') =>
      api(METHOD.respondDialog, instanceId, { dialogId, buttonId, listItem, inputText }),
    clickPlayer: (playerId) => api(METHOD.clickPlayer, instanceId, { playerId }),
    clickTextDraw: (textDrawId) => api(METHOD.clickTextDraw, instanceId, { textDrawId }),
    action: (action, params = {}) => api(METHOD.action, instanceId, { action, params }),
  }
}

async function runDebug(message) {
  try {
    const context = {
      api: instanceApi(message.instanceId),
      event: null,
      on,
      instanceApi,
      dispatch,
      state: debugState,
      log,
      console: { log: (value) => log(LOG_LEVEL_INFO, value) },
    }
    const script = new vm.Script(`(async () => {\n${message.code}\n})()`)
    const result = await withTimeout(
      script.runInNewContext(context, { timeout: DEBUG_SYNC_TIMEOUT_MS }),
      DEBUG_ASYNC_TIMEOUT_MS,
      `debug execution timed out after ${DEBUG_ASYNC_TIMEOUT_MS}ms`,
    )
    write({ type: MESSAGE_DEBUG_RESULT, id: message.id, result })
  } catch (error) {
    write({ type: MESSAGE_DEBUG_RESULT, id: message.id, error: error.stack ?? error.message ?? String(error) })
  }
}

export async function start() {
  const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
  write({ type: MESSAGE_READY, protocol: PROTOCOL_VERSION, events: handlers.map((handler) => handler.pattern) })
  for await (const line of rl) {
    if (!line.trim()) continue
    let message
    try {
      message = JSON.parse(line)
    } catch (error) {
      log(LOG_LEVEL_ERROR, `invalid host message: ${error.message}`)
      continue
    }
    if (message.type === MESSAGE_RESULT || message.type === MESSAGE_DEBUG_RESULT) {
      const waiter = pending.get(message.id)
      if (!waiter) continue
      pending.delete(message.id)
      clearTimeout(waiter.timer)
      if (message.error) waiter.reject(new Error(message.error))
      else waiter.resolve(message.result)
      continue
    }
    if (message.type === MESSAGE_EVENT) {
      const event = { name: message.name, instanceId: message.instanceId, time: message.time, data: message.data }
      void dispatch(event)
      continue
    }
    if (message.type === MESSAGE_DEBUG) {
      void runDebug(message)
    }
  }
  const error = new Error('plugin host disconnected')
  for (const waiter of pending.values()) {
    clearTimeout(waiter.timer)
    waiter.reject(error)
  }
  pending.clear()
}
