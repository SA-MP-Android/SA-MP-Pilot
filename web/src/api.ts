import { API_PREFIX, EVENTS_PATH, RECONNECT_INITIAL_MS, RECONNECT_MAX_MS } from './constants'
import { CHAT_PAGE_SIZE } from './constants'
import type {
  ChatPage,
  Event,
  InstancePatch,
  PluginDebugResult,
  PluginInfo,
  QuickCommand,
  Server,
  Snapshot,
} from './types'

const JSON_CONTENT_TYPE = 'application/json'
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_PREFIX}${path}`, {
    headers: { 'Content-Type': JSON_CONTENT_TYPE },
    ...init,
  })
  if (!response.ok) {
    const problem = await response.json().catch(() => ({ error: response.statusText }))
    throw new Error(problem.error)
  }
  const body = await response.text()
  return body.trim() === '' ? (undefined as T) : (JSON.parse(body) as T)
}
const instancePath = (id: string) => `/instances/${id}`
export function normalizeSnapshot(value: Snapshot): Snapshot {
  return {
    ...value,
    server: value.server
      ? {
          ...value.server,
          // Older persisted servers do not have this optional compatibility flag.
          emulatePcClientCheck: value.server.emulatePcClientCheck ?? false,
        }
      : value.server,
    revision: value.revision ?? 0,
    syncEpoch: value.syncEpoch ?? '',
    chat: value.chat ?? [],
    players: value.players ?? [],
    nearbyPlayers: value.nearbyPlayers ?? [],
    vehicles: value.vehicles ?? [],
    objects: value.objects ?? [],
    textDraws: value.textDraws ?? [],
    dialogs: value.dialogs ?? [],
    commands: value.commands ?? [],
    localPlayer: value.localPlayer ?? { id: -1, health: 0, armour: 0 },
    spawnReady: value.spawnReady ?? false,
  }
}
export function upsertSnapshot(items: Snapshot[], snapshot: Snapshot): Snapshot[] {
  const index = items.findIndex((item) => item.server.id === snapshot.server.id)
  if (index < 0) return [...items, snapshot]
  return items.map((item, itemIndex) => {
    if (itemIndex !== index) return item
    // Revisions are scoped to syncEpoch. A new epoch is authoritative even
    // when its revision is lower because the backend may have restarted.
    if (snapshot.syncEpoch === item.syncEpoch && snapshot.revision < item.revision) return item
    return { ...snapshot, chat: (snapshot.chat?.length ?? 0) > 0 ? snapshot.chat : (item.chat ?? []) }
  })
}

export function isStaleInstancePatch(snapshot: Snapshot, patch: InstancePatch): boolean {
  return patch.syncEpoch === snapshot.syncEpoch && patch.revision <= snapshot.revision
}

// Returns null when a patch cannot be safely applied. Callers must then fetch a
// full snapshot; this is essential because the server intentionally drops
// stale websocket state events under load.
export function applyInstancePatch(snapshot: Snapshot, patch: InstancePatch): Snapshot | null {
  if (patch.syncEpoch !== snapshot.syncEpoch || patch.revision !== snapshot.revision + 1) return null
  const next = { ...snapshot }
  for (const operation of patch.operations) {
    if (operation.op !== 'replace') return null
    switch (operation.path) {
      case '/server':
        next.server = operation.value as Snapshot['server']
        break
      case '/connection':
        next.connection = operation.value as Snapshot['connection']
        break
      case '/players':
        next.players = operation.value as Snapshot['players']
        break
      case '/nearbyPlayers':
        next.nearbyPlayers = operation.value as Snapshot['nearbyPlayers']
        break
      case '/vehicles':
        next.vehicles = operation.value as Snapshot['vehicles']
        break
      case '/objects':
        next.objects = operation.value as Snapshot['objects']
        break
      case '/textDraws':
        next.textDraws = operation.value as Snapshot['textDraws']
        break
      case '/dialogs':
        next.dialogs = operation.value as Snapshot['dialogs']
        break
      case '/commands':
        next.commands = operation.value as Snapshot['commands']
        break
      case '/activeDialog':
        next.activeDialog = operation.value as Snapshot['activeDialog']
        break
      case '/localPlayer':
        next.localPlayer = operation.value as Snapshot['localPlayer']
        break
      case '/vehicleState':
        next.vehicleState = operation.value as Snapshot['vehicleState']
        break
      case '/keyMask':
        next.keyMask = operation.value as number
        break
      case '/afk':
        next.afk = operation.value as boolean
        break
      case '/spawned':
        next.spawned = operation.value as boolean
        break
      case '/spawnReady':
        next.spawnReady = operation.value as boolean
        break
      default:
        return null
    }
  }
  return normalizeSnapshot({ ...next, revision: patch.revision })
}
export const api = {
  list: () =>
    request<Snapshot[] | null>('/instances', { cache: 'no-store' }).then((items) =>
      (items ?? []).map(normalizeSnapshot),
    ),
  get: (id: string) => request<Snapshot>(instancePath(id), { cache: 'no-store' }).then(normalizeSnapshot),
  create: (server: Omit<Server, 'id'>) =>
    request<Snapshot>('/instances', { method: 'POST', body: JSON.stringify(server) }).then(normalizeSnapshot),
  remove: (id: string) => request<void>(instancePath(id), { method: 'DELETE' }),
  update: (id: string, server: Omit<Server, 'id'>) =>
    request<Snapshot>(instancePath(id), { method: 'PUT', body: JSON.stringify(server) }).then(
      normalizeSnapshot,
    ),
  addCommand: (id: string, command: Pick<QuickCommand, 'label' | 'command'>) =>
    request<QuickCommand>(`${instancePath(id)}/commands`, { method: 'POST', body: JSON.stringify(command) }),
  removeCommand: (id: string, commandId: string) =>
    request<void>(`${instancePath(id)}/commands/${commandId}`, { method: 'DELETE' }),
  connect: (id: string) => request<void>(`${instancePath(id)}/connect`, { method: 'POST' }),
  disconnect: (id: string) => request<void>(`${instancePath(id)}/disconnect`, { method: 'POST' }),
  chat: (id: string, before?: number) => {
    const query = new URLSearchParams({ limit: String(CHAT_PAGE_SIZE) })
    if (before) query.set('before', String(before))
    return request<ChatPage>(`${instancePath(id)}/chat?${query}`)
  },
  action: (id: string, action: string, data: unknown = {}) =>
    request<void>(`${instancePath(id)}/actions/${action}`, { method: 'POST', body: JSON.stringify(data) }),
  plugins: () => request<PluginInfo[]>('/plugins', { cache: 'no-store' }),
  debugPlugin: (id: string, instanceId: string, code: string) =>
    request<PluginDebugResult>(`/plugins/${encodeURIComponent(id)}/debug`, {
      method: 'POST',
      body: JSON.stringify({ instanceId, code }),
    }),
  startPlugin: (id: string) => request<void>(`/plugins/${encodeURIComponent(id)}/start`, { method: 'POST' }),
  stopPlugin: (id: string) => request<void>(`/plugins/${encodeURIComponent(id)}/stop`, { method: 'POST' }),
  restartPlugin: (id: string) =>
    request<void>(`/plugins/${encodeURIComponent(id)}/restart`, { method: 'POST' }),
}
export function events(onEvent: (event: Event) => void, onOpen?: () => void) {
  const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
  let closed = false
  let retry = RECONNECT_INITIAL_MS
  let socket: WebSocket
  const open = () => {
    if (closed) return
    socket = new WebSocket(`${protocol}://${location.host}${API_PREFIX}${EVENTS_PATH}`)
    socket.onmessage = (message) => {
      const event = JSON.parse(message.data) as Event
      onEvent(event)
    }
    socket.onopen = () => {
      retry = RECONNECT_INITIAL_MS
      onOpen?.()
    }
    socket.onclose = () => {
      if (!closed) {
        setTimeout(open, retry)
        retry = Math.min(retry * 2, RECONNECT_MAX_MS)
      }
    }
  }
  open()
  return () => {
    closed = true
    socket?.close()
  }
}
