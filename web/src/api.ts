import { API_PREFIX, EVENTS_PATH, RECONNECT_INITIAL_MS, RECONNECT_MAX_MS } from './constants'
import type { Event, QuickCommand, Server, Snapshot } from './types'

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
    chat: value.chat ?? [],
    players: value.players ?? [],
    nearbyPlayers: value.nearbyPlayers ?? [],
    vehicles: value.vehicles ?? [],
    objects: value.objects ?? [],
    textDraws: value.textDraws ?? [],
    dialogs: value.dialogs ?? [],
    commands: value.commands ?? [],
  }
}
export function upsertSnapshot(items: Snapshot[], snapshot: Snapshot): Snapshot[] {
  const index = items.findIndex((item) => item.server.id === snapshot.server.id)
  if (index < 0) return [...items, snapshot]
  return items.map((item, itemIndex) => (itemIndex === index ? snapshot : item))
}
export const api = {
  list: () => request<Snapshot[] | null>('/instances').then((items) => (items ?? []).map(normalizeSnapshot)),
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
  action: (id: string, action: string, data: unknown = {}) =>
    request<void>(`${instancePath(id)}/actions/${action}`, { method: 'POST', body: JSON.stringify(data) }),
}
export function events(onEvent: (event: Event) => void) {
  const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
  let closed = false
  let retry = RECONNECT_INITIAL_MS
  let socket: WebSocket
  const open = () => {
    socket = new WebSocket(`${protocol}://${location.host}${API_PREFIX}${EVENTS_PATH}`)
    socket.onmessage = (message) => {
      const event = JSON.parse(message.data) as Event
      onEvent(event.data ? { ...event, data: normalizeSnapshot(event.data) } : event)
    }
    socket.onopen = () => {
      retry = RECONNECT_INITIAL_MS
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
