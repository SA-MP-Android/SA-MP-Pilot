import { afterEach, describe, expect, it, vi } from 'vitest'
import { EVENT_INSTANCE_UPDATED } from '@/constants'
import type { Event, Snapshot } from '@/types'
import { InstanceSyncController, type InstanceSyncSource } from './instance-sync'

afterEach(() => vi.useRealTimers())

function snapshot(revision: number, syncEpoch = 'epoch', overrides: Partial<Snapshot> = {}): Snapshot {
  return {
    revision,
    syncEpoch,
    server: {
      id: 'instance',
      host: '127.0.0.1',
      port: 7777,
      nickname: 'tester',
      password: '',
      encoding: 'utf-8',
      autoConnect: false,
    },
    connection: { status: 'connected', serverName: '', error: '', playerCount: 0, maxPlayers: 0 },
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
    ...overrides,
  }
}

function patch(revision: number, syncEpoch = 'epoch', value = true): Event {
  return {
    type: EVENT_INSTANCE_UPDATED,
    instanceId: 'instance',
    data: {
      revision,
      syncEpoch,
      operations: [{ op: 'replace', path: '/afk', value }],
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function source(overrides: Partial<InstanceSyncSource> = {}): InstanceSyncSource {
  return {
    list: async () => [],
    get: async () => snapshot(1),
    ...overrides,
  }
}

describe('InstanceSyncController', () => {
  it('replays events received while the full bootstrap is in flight', async () => {
    const list = deferred<Snapshot[]>()
    const changes: Snapshot[][] = []
    const controller = new InstanceSyncController(source({ list: () => list.promise }), (items) =>
      changes.push(items),
    )

    const reconciliation = controller.reconcile()
    controller.handle(patch(2))
    list.resolve([snapshot(1)])
    await reconciliation

    expect(controller.current[0]).toMatchObject({ revision: 2, afk: true })
    expect(changes.at(-1)?.[0]).toMatchObject({ revision: 2, afk: true })
    controller.dispose()
  })

  it('extends an in-flight resync target instead of dropping later patches', async () => {
    const first = deferred<Snapshot>()
    const second = deferred<Snapshot>()
    let gets = 0
    const controller = new InstanceSyncController(
      source({
        get: () => {
          gets += 1
          return gets === 1 ? first.promise : second.promise
        },
      }),
      () => undefined,
    )
    controller.acceptSnapshot(snapshot(1))

    controller.handle(patch(3))
    await Promise.resolve()
    controller.handle(patch(4))
    first.resolve(snapshot(3))
    await new Promise((resolve) => setTimeout(resolve, 60))
    expect(gets).toBe(2)

    second.resolve(snapshot(4, 'epoch', { afk: true }))
    await Promise.resolve()
    await Promise.resolve()
    expect(controller.current[0]).toMatchObject({ revision: 4, afk: true })
    controller.dispose()
  })

  it('accepts a lower revision from a new server epoch during resync', async () => {
    const current = snapshot(10, 'old-epoch')
    const controller = new InstanceSyncController(
      source({ get: async () => snapshot(2, 'new-epoch', { afk: true }) }),
      () => undefined,
    )
    controller.acceptSnapshot(current)

    controller.handle(patch(2, 'new-epoch'))
    await Promise.resolve()
    await Promise.resolve()

    expect(controller.current[0]).toMatchObject({ revision: 2, syncEpoch: 'new-epoch', afk: true })
    controller.dispose()
  })

  it('resynchronizes an update for an instance not present in the local baseline', async () => {
    const controller = new InstanceSyncController(
      source({ get: async () => snapshot(2, 'epoch', { afk: true }) }),
      () => undefined,
    )

    controller.handle(patch(2))
    await Promise.resolve()
    await Promise.resolve()

    expect(controller.current[0]).toMatchObject({ revision: 2, afk: true })
    controller.dispose()
  })
})
