import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, normalizeSnapshot, upsertSnapshot } from './api'
import type { Snapshot } from './types'
afterEach(() => vi.restoreAllMocks())
describe('api', () => {
  it('surfaces backend errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: 'bad server' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    )
    await expect(api.list()).rejects.toThrow('bad server')
  })

  it('normalizes nullable collections from persisted legacy data', () => {
    const legacy = {
      chat: null,
      players: null,
      nearbyPlayers: null,
      vehicles: null,
      objects: null,
      textDraws: null,
      dialogs: null,
      commands: null,
    } as unknown as Snapshot
    const snapshot = normalizeSnapshot(legacy)
    expect(snapshot.chat).toEqual([])
    expect(snapshot.players).toEqual([])
    expect(snapshot.commands).toEqual([])
  })

  it('normalizes a null instance list', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          new Response('null', { status: 200, headers: { 'Content-Type': 'application/json' } }),
        ),
    )
    await expect(api.list()).resolves.toEqual([])
  })

  it('accepts a successful response with an empty body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 202 })))
    await expect(api.connect('instance-id')).resolves.toBeUndefined()
  })

  it('replaces an instance delivered by both create and websocket events', () => {
    const first = { server: { id: 'same-instance' } } as Snapshot
    const latest = { server: { id: 'same-instance' } } as Snapshot
    expect(upsertSnapshot([first], latest)).toEqual([latest])
  })

  it('updates an instance without changing list order', () => {
    const first = { server: { id: 'first' } } as Snapshot
    const second = { server: { id: 'second' } } as Snapshot
    const updatedFirst = { server: { id: 'first' } } as Snapshot
    expect(upsertSnapshot([first, second], updatedFirst)).toEqual([updatedFirst, second])
  })
})
