import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, applyInstancePatch, normalizeSnapshot, upsertSnapshot } from './api'
import type { InstancePatch, Snapshot } from './types'
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
    expect(snapshot.localPlayer).toEqual({ id: -1, health: 0, armour: 0 })
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
    const first = { server: { id: 'same-instance' }, chat: [] } as unknown as Snapshot
    const latest = { server: { id: 'same-instance' }, chat: [] } as unknown as Snapshot
    expect(upsertSnapshot([first], latest)).toEqual([latest])
  })

  it('updates an instance without changing list order', () => {
    const first = { server: { id: 'first' }, chat: [] } as unknown as Snapshot
    const second = { server: { id: 'second' }, chat: [] } as unknown as Snapshot
    const updatedFirst = { server: { id: 'first' }, chat: [] } as unknown as Snapshot
    expect(upsertSnapshot([first, second], updatedFirst)).toEqual([updatedFirst, second])
  })

  it('preserves chat when a websocket snapshot omits chat history', () => {
    const chat = [{ id: 1, text: 'hello', color: '#ffffffff', at: new Date().toISOString() }]
    const current = { server: { id: 'instance' }, chat } as unknown as Snapshot
    const update = { server: { id: 'instance' }, chat: [] } as unknown as Snapshot
    expect(upsertSnapshot([current], update)[0].chat).toEqual(chat)
  })

  it('accepts a lower revision when the backend starts a new sync epoch', () => {
    const current = {
      server: { id: 'instance' },
      revision: 10,
      syncEpoch: 'old',
      chat: [],
    } as unknown as Snapshot
    const restarted = {
      server: { id: 'instance' },
      revision: 1,
      syncEpoch: 'new',
      chat: [],
    } as unknown as Snapshot
    expect(upsertSnapshot([current], restarted)[0]).toMatchObject({ revision: 1, syncEpoch: 'new' })
  })

  it('applies only the fields included in a contiguous instance patch', () => {
    const current = {
      revision: 7,
      syncEpoch: 'epoch',
      server: { id: 'instance' },
      connection: { status: 'connecting' },
      chat: [],
      players: [],
      nearbyPlayers: [],
      vehicles: [],
      objects: [],
      textDraws: [],
      dialogs: [],
      commands: [],
      activeDialog: null,
      vehicleState: { inVehicle: false, passenger: false, vehicleId: -1 },
      keyMask: 0,
      afk: false,
      spawned: false,
      spawnReady: false,
    } as unknown as Snapshot
    const patch: InstancePatch = {
      revision: 8,
      syncEpoch: 'epoch',
      operations: [
        { op: 'replace', path: '/connection', value: { status: 'connected' } },
        { op: 'replace', path: '/localPlayer', value: { id: 7, health: 73.5, armour: 20 } },
        { op: 'replace', path: '/vehicleState', value: { inVehicle: true, passenger: false, vehicleId: 42, health: 0, healthKnown: true } },
        { op: 'replace', path: '/spawned', value: true },
        { op: 'replace', path: '/spawnReady', value: true },
      ],
    }
    expect(applyInstancePatch(current, patch)).toMatchObject({
      revision: 8,
      connection: { status: 'connected' },
      localPlayer: { id: 7, health: 73.5, armour: 20 },
      vehicleState: { inVehicle: true, passenger: false, vehicleId: 42, health: 0, healthKnown: true },
      spawned: true,
      spawnReady: true,
      server: { id: 'instance' },
    })
  })

  it('rejects a skipped or unsupported patch so the caller can resync', () => {
    const current = { revision: 7 } as Snapshot
    expect(applyInstancePatch(current, { revision: 9, syncEpoch: 'epoch', operations: [] })).toBeNull()
    expect(
      applyInstancePatch(current, {
        revision: 8,
        syncEpoch: 'epoch',
        operations: [{ op: 'replace', path: '/unknown' as '/server', value: {} }],
      }),
    ).toBeNull()
  })
})
