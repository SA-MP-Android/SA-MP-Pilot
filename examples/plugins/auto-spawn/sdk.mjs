import readline from 'node:readline'
import vm from 'node:vm'

export const PROTOCOL_VERSION = 1
export const MAX_PROTOCOL_MESSAGE_BYTES = 1 << 20
export const MAX_LOG_MESSAGE_BYTES = 16 * 1024
export const MAX_SUBSCRIPTIONS = 256
export const MAX_SUBSCRIPTION_BYTES = 256
export const API_REQUEST_TIMEOUT_MS = 15_000
export const DEBUG_SYNC_TIMEOUT_MS = 5_000
export const DEBUG_ASYNC_TIMEOUT_MS = 15_000
export const LOG_LEVEL_INFO = 'info'
export const LOG_LEVEL_WARN = 'warn'
export const LOG_LEVEL_ERROR = 'error'
export const VEHICLE_ENTRY_DIRECT = 'direct'
export const VEHICLE_ENTRY_NORMAL = 'normal'

const MESSAGE_READY = 'ready'
const MESSAGE_EVENT = 'event'
const MESSAGE_CALL = 'call'
const MESSAGE_RESULT = 'result'
const MESSAGE_LOG = 'log'
const MESSAGE_DEBUG = 'debug'
const MESSAGE_DEBUG_RESULT = 'debug.result'
const MESSAGE_SUBSCRIBE = 'subscribe'

export const EVENT_INSTANCE_CREATED = 'instance.created'
export const EVENT_INSTANCE_UPDATED = 'instance.updated'
export const EVENT_INSTANCE_DELETED = 'instance.deleted'
export const EVENT_CHAT_MESSAGE = 'chat.message'
export const EVENT_CHAT_RESET = 'chat.reset'
export const EVENT_CLIENT_JOINED = 'client.joined'
export const EVENT_CLIENT_CHAT = 'client.chat'
export const EVENT_CLIENT_PLAYER_JOIN = 'client.player.join'
export const EVENT_CLIENT_PLAYER_QUIT = 'client.player.quit'
export const EVENT_CLIENT_SCORES = 'client.scores'
export const EVENT_CLIENT_DIALOG = 'client.dialog'
export const EVENT_CLIENT_DISCONNECTED = 'client.disconnected'
export const EVENT_CLIENT_PROTOCOL_ERROR = 'client.protocol.error'
export const EVENT_CLIENT_TEXTDRAW_SHOW = 'client.textdraw.show'
export const EVENT_CLIENT_TEXTDRAW_HIDE = 'client.textdraw.hide'
export const EVENT_CLIENT_TEXTDRAW_TEXT = 'client.textdraw.text'
export const EVENT_CLIENT_OBJECT_ADD = 'client.object.add'
export const EVENT_CLIENT_OBJECT_REMOVE = 'client.object.remove'
export const EVENT_CLIENT_VEHICLE_ADD = 'client.vehicle.add'
export const EVENT_CLIENT_VEHICLE_REMOVE = 'client.vehicle.remove'
export const EVENT_CLIENT_PLAYER_SYNC = 'client.player.sync'
export const EVENT_CLIENT_POSITION = 'client.position'
export const EVENT_CLIENT_APPEARANCE = 'client.appearance'
export const EVENT_CLIENT_PLAYER_HEALTH = 'client.player.health'
export const EVENT_CLIENT_PLAYER_LIFE_STATE = 'client.player.state'
export const EVENT_CLIENT_PLAYER_DEATH = 'client.player.death'
export const EVENT_CLIENT_VEHICLE_STATE = 'client.vehicle.state'
export const EVENT_CLIENT_VEHICLE_HEALTH = 'client.vehicle.health'
export const EVENT_CLIENT_SPAWNED = 'client.spawned'
export const EVENT_CLIENT_VEHICLE_SYNC = 'client.vehicle.sync'
export const EVENT_CLIENT_MOVEMENT_STARTED = 'client.movement.started'
export const EVENT_CLIENT_MOVEMENT_PROGRESS = 'client.movement.progress'
export const EVENT_CLIENT_MOVEMENT_COMPLETED = 'client.movement.completed'
export const EVENT_CLIENT_MOVEMENT_STOPPED = 'client.movement.stopped'
export const EVENT_CLIENT_MOVEMENT_FAILED = 'client.movement.failed'

export const METHOD = Object.freeze({
  listInstances: 'instances.list',
  createInstance: 'instances.create',
  updateInstance: 'instances.update',
  deleteInstance: 'instances.delete',
  getSnapshot: 'instance.getSnapshot',
  getChat: 'instance.getChat',
  connect: 'instance.connect',
  disconnect: 'instance.disconnect',
  sendChat: 'instance.sendChat',
  sendCommand: 'instance.sendCommand',
  requestSpawn: 'instance.requestSpawn',
  refreshScores: 'instance.refreshScores',
  setKeys: 'instance.setKeys',
  setAFK: 'instance.setAFK',
  teleport: 'instance.teleport',
  enterVehicle: 'instance.enterVehicle',
  exitVehicle: 'instance.exitVehicle',
  walkTo: 'instance.walkTo',
  driveTo: 'instance.driveTo',
  stopMovement: 'instance.stopMovement',
  respondDialog: 'instance.respondDialog',
  clickPlayer: 'instance.clickPlayer',
  clickTextDraw: 'instance.clickTextDraw',
  action: 'instance.action',
  addCommand: 'instance.commands.add',
  deleteCommand: 'instance.commands.delete',
})

export const EVENTS = Object.freeze({
  instanceCreated: EVENT_INSTANCE_CREATED,
  instanceUpdated: EVENT_INSTANCE_UPDATED,
  instanceDeleted: EVENT_INSTANCE_DELETED,
  chatMessage: EVENT_CHAT_MESSAGE,
  chatReset: EVENT_CHAT_RESET,
  clientJoined: EVENT_CLIENT_JOINED,
  clientChat: EVENT_CLIENT_CHAT,
  clientPlayerJoin: EVENT_CLIENT_PLAYER_JOIN,
  clientPlayerQuit: EVENT_CLIENT_PLAYER_QUIT,
  clientScores: EVENT_CLIENT_SCORES,
  clientDialog: EVENT_CLIENT_DIALOG,
  clientDisconnected: EVENT_CLIENT_DISCONNECTED,
  clientProtocolError: EVENT_CLIENT_PROTOCOL_ERROR,
  clientTextDrawShow: EVENT_CLIENT_TEXTDRAW_SHOW,
  clientTextDrawHide: EVENT_CLIENT_TEXTDRAW_HIDE,
  clientTextDrawText: EVENT_CLIENT_TEXTDRAW_TEXT,
  clientObjectAdd: EVENT_CLIENT_OBJECT_ADD,
  clientObjectRemove: EVENT_CLIENT_OBJECT_REMOVE,
  clientVehicleAdd: EVENT_CLIENT_VEHICLE_ADD,
  clientVehicleRemove: EVENT_CLIENT_VEHICLE_REMOVE,
  clientPlayerSync: EVENT_CLIENT_PLAYER_SYNC,
  clientPosition: EVENT_CLIENT_POSITION,
  clientAppearance: EVENT_CLIENT_APPEARANCE,
  clientPlayerHealth: EVENT_CLIENT_PLAYER_HEALTH,
  clientPlayerLifeState: EVENT_CLIENT_PLAYER_LIFE_STATE,
  clientPlayerDeath: EVENT_CLIENT_PLAYER_DEATH,
  clientVehicleState: EVENT_CLIENT_VEHICLE_STATE,
  clientVehicleHealth: EVENT_CLIENT_VEHICLE_HEALTH,
  clientSpawned: EVENT_CLIENT_SPAWNED,
  clientVehicleSync: EVENT_CLIENT_VEHICLE_SYNC,
  clientMovementStarted: EVENT_CLIENT_MOVEMENT_STARTED,
  clientMovementProgress: EVENT_CLIENT_MOVEMENT_PROGRESS,
  clientMovementCompleted: EVENT_CLIENT_MOVEMENT_COMPLETED,
  clientMovementStopped: EVENT_CLIENT_MOVEMENT_STOPPED,
  clientMovementFailed: EVENT_CLIENT_MOVEMENT_FAILED,
})

const handlers = []
const pending = new Map()
const debugState = Object.create(null)
let nextId = 0
let started = false

function truncateUtf8(value, maxBytes) {
  const text = String(value)
  if (Buffer.byteLength(text, 'utf8') <= maxBytes) return text
  let result = ''
  let size = 0
  for (const character of text) {
    const characterSize = Buffer.byteLength(character, 'utf8')
    if (size + characterSize > maxBytes) break
    result += character
    size += characterSize
  }
  return result
}

function encodeMessage(message) {
  let encoded
  try {
    encoded = JSON.stringify(message)
  } catch (error) {
    throw new Error(`plugin message is not JSON serializable: ${error.message}`)
  }
  if (typeof encoded !== 'string') throw new Error('plugin message is not JSON serializable')
  if (Buffer.byteLength(encoded, 'utf8') + 1 > MAX_PROTOCOL_MESSAGE_BYTES) {
    throw new Error('plugin protocol message is too large')
  }
  return encoded
}

function write(message) {
  process.stdout.write(`${encodeMessage(message)}\n`)
}

export function log(level, message) {
  try {
    write({ type: MESSAGE_LOG, level: String(level || 'info'), message: truncateUtf8(message, MAX_LOG_MESSAGE_BYTES) })
  } catch {
    // stdout is reserved for protocol messages; a failed log must not corrupt it.
  }
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
  log: (...values) => log('info', values.map(displayValue).join(' ')),
  info: (...values) => log('info', values.map(displayValue).join(' ')),
  warn: (...values) => log('warn', values.map(displayValue).join(' ')),
  error: (...values) => log('error', values.map(displayValue).join(' ')),
}

export function api(method, instanceId = '', params = {}) {
  const id = ++nextId
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id)
      reject(new Error(`plugin API request timed out after ${API_REQUEST_TIMEOUT_MS}ms`))
    }, API_REQUEST_TIMEOUT_MS)
    pending.set(id, { resolve, reject, timer })
    try {
      write({ type: MESSAGE_CALL, id, method, instanceId, params })
    } catch (error) {
      clearTimeout(timer)
      pending.delete(id)
      reject(error)
    }
  })
}

function matches(pattern, name) {
  return pattern === '*' || pattern === name || (pattern.endsWith('*') && name.startsWith(pattern.slice(0, -1)))
}

function normalizePattern(pattern) {
  if (typeof pattern !== 'string' || pattern.trim() === '') throw new TypeError('event pattern must be a non-empty string')
  const normalized = pattern.trim()
  if (Buffer.byteLength(normalized, 'utf8') > MAX_SUBSCRIPTION_BYTES) throw new RangeError('event pattern is too large')
  const wildcard = normalized.indexOf('*')
  const isSuffixWildcard = wildcard === normalized.length - 1 && normalized.indexOf('*', wildcard + 1) < 0
  if (wildcard >= 0 && normalized !== '*' && !isSuffixWildcard) {
    throw new TypeError('event pattern must be an exact name, a suffix wildcard, or *')
  }
  return normalized
}

export function on(pattern, handler) {
  const subscription = { pattern: normalizePattern(pattern), handler }
  if (typeof handler !== 'function') throw new TypeError('event handler must be a function')
  if (handlers.length >= MAX_SUBSCRIPTIONS) throw new RangeError('too many event subscriptions')
  handlers.push(subscription)
  if (started) write({ type: MESSAGE_SUBSCRIBE, events: handlers.map((item) => item.pattern) })
  return () => {
    const index = handlers.indexOf(subscription)
    if (index < 0) return
    handlers.splice(index, 1)
    if (started) write({ type: MESSAGE_SUBSCRIBE, events: handlers.map((item) => item.pattern) })
  }
}

export async function dispatch(event) {
  if (!event || typeof event.name !== 'string') throw new TypeError('event.name must be a string')
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
        Promise.resolve()
          .then(() => handler.handler(normalized, instanceApi(normalized.instanceId)))
          .catch((error) =>
            log('error', `${normalized.name}: ${error?.stack ?? error?.message ?? error}`),
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
    connect: () => api(METHOD.connect, instanceId),
    disconnect: () => api(METHOD.disconnect, instanceId),
    sendChat: (text) => api(METHOD.sendChat, instanceId, { text }),
    sendCommand: (command) => api(METHOD.sendCommand, instanceId, { command }),
    requestSpawn: () => api(METHOD.requestSpawn, instanceId),
    refreshScores: () => api(METHOD.refreshScores, instanceId),
    setKeys: (mask) => api(METHOD.setKeys, instanceId, { mask }),
    setAFK: (enabled) => api(METHOD.setAFK, instanceId, { enabled }),
    teleport: (x, y, z) => api(METHOD.teleport, instanceId, { x, y, z }),
    enterVehicle: (vehicleId, passenger = false, mode = VEHICLE_ENTRY_DIRECT) =>
      api(METHOD.enterVehicle, instanceId, { vehicleId, passenger, mode }),
    exitVehicle: () => api(METHOD.exitVehicle, instanceId),
    walkTo: (x, y, z, options = {}) => api(METHOD.walkTo, instanceId, { ...options, x, y, z }),
    driveTo: (x, y, z, options = {}) => api(METHOD.driveTo, instanceId, { ...options, x, y, z }),
    stopMovement: () => api(METHOD.stopMovement, instanceId),
    respondDialog: (dialogId, buttonId, listItem = 0, inputText = '') =>
      api(METHOD.respondDialog, instanceId, { dialogId, buttonId, listItem, inputText }),
    clickPlayer: (playerId) => api(METHOD.clickPlayer, instanceId, { playerId }),
    clickTextDraw: (textDrawId) => api(METHOD.clickTextDraw, instanceId, { textDrawId }),
    addCommand: (command) => api(METHOD.addCommand, instanceId, command),
    deleteCommand: (commandId) => api(METHOD.deleteCommand, instanceId, { commandId }),
    action: (action, params = {}) => api(METHOD.action, instanceId, { action, params }),
  }
}

export function instancesApi() {
  return {
    list: () => api(METHOD.listInstances),
    create: (server) => api(METHOD.createInstance, '', server),
    update: (instanceId, server) => api(METHOD.updateInstance, instanceId, server),
    delete: (instanceId) => api(METHOD.deleteInstance, instanceId),
  }
}

async function runDebug(message) {
  try {
    const context = {
      api: instanceApi(message.instanceId),
      event: null,
      on,
      instanceApi,
      instancesApi,
      dispatch,
      state: debugState,
      log,
      setTimeout,
      clearTimeout,
      console: {
        log: (...values) => log('info', values.map(displayValue).join(' ')),
        info: (...values) => log('info', values.map(displayValue).join(' ')),
        warn: (...values) => log('warn', values.map(displayValue).join(' ')),
        error: (...values) => log('error', values.map(displayValue).join(' ')),
      },
    }
    const script = new vm.Script(`(async () => {\n${message.code}\n})()`)
    const result = await withTimeout(
      script.runInNewContext(context, { timeout: DEBUG_SYNC_TIMEOUT_MS }),
      DEBUG_ASYNC_TIMEOUT_MS,
      `debug execution timed out after ${DEBUG_ASYNC_TIMEOUT_MS}ms`,
    )
    try {
      write({ type: MESSAGE_DEBUG_RESULT, id: message.id, result })
    } catch (error) {
      write({ type: MESSAGE_DEBUG_RESULT, id: message.id, error: truncateUtf8(error.message, MAX_LOG_MESSAGE_BYTES) })
    }
  } catch (error) {
    try {
      write({ type: MESSAGE_DEBUG_RESULT, id: message.id, error: truncateUtf8(error?.stack ?? error?.message ?? error, MAX_LOG_MESSAGE_BYTES) })
    } catch {
      // The host will terminate the request if even the error response cannot be encoded.
    }
  }
}

export async function start() {
  if (started) throw new Error('plugin SDK start() may only be called once')
  started = true
  const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
  write({ type: MESSAGE_READY, protocol: PROTOCOL_VERSION, events: handlers.map((handler) => handler.pattern) })
  for await (const line of rl) {
    if (!line.trim()) continue
    let message
    try {
      message = JSON.parse(line)
    } catch (error) {
      log('error', `invalid host message: ${error.message}`)
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
      void dispatch({ name: message.name, instanceId: message.instanceId, time: message.time, data: message.data })
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
