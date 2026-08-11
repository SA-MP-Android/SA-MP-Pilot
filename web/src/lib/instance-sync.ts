import {
  EVENT_CHAT_MESSAGE,
  EVENT_CHAT_RESET,
  EVENT_INSTANCE_CREATED,
  EVENT_INSTANCE_DELETED,
  EVENT_INSTANCE_UPDATED,
  MAX_CHAT_MESSAGES,
} from '@/constants'
import { applyInstancePatch, isStaleInstancePatch, normalizeSnapshot, upsertSnapshot } from '@/api'
import type { ChatMessage, Event, InstancePatch, Snapshot } from '@/types'

export interface InstanceSyncSource {
  list: () => Promise<Snapshot[]>
  get: (id: string) => Promise<Snapshot>
}

type SnapshotUpdater = (snapshot: Snapshot) => Snapshot
type SyncCursor = Pick<InstancePatch, 'revision' | 'syncEpoch'>
type QueuedAction = () => void

const RETRY_INITIAL_MS = 250
const RETRY_MAX_MS = 5_000
const STALE_SNAPSHOT_RETRY_MS = 50

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value))
}

function newerCursor(candidate: SyncCursor, current: SyncCursor | undefined): boolean {
  if (!current) return true
  if (candidate.syncEpoch !== current.syncEpoch) return true
  return candidate.revision > current.revision
}

function isPatch(value: unknown): value is InstancePatch {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<InstancePatch>
  return (
    typeof candidate.revision === 'number' &&
    typeof candidate.syncEpoch === 'string' &&
    Array.isArray(candidate.operations)
  )
}

function isSnapshot(value: unknown): value is Snapshot {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<Snapshot>
  return Boolean(candidate.server && typeof candidate.server.id === 'string')
}

function sameInstance(left: Snapshot, right: Snapshot): boolean {
  return left.server.id === right.server.id
}

/**
 * Owns the client-side instance synchronization protocol.
 *
 * There are two synchronization scopes:
 * - reconcile() establishes a full baseline and queues every event received
 *   while that baseline is in flight;
 * - per-instance resyncs recover from a revision gap and keep extending their
 *   target while more patches arrive.
 *
 * Keeping this state outside React setState callbacks makes the event stream
 * deterministic and prevents asynchronous callbacks from silently discarding
 * updates.
 */
export class InstanceSyncController {
  private readonly source: InstanceSyncSource
  private readonly onChange: (snapshots: Snapshot[]) => void
  private readonly onError: (error: Error) => void
  private readonly onChatReset: (id: string) => void
  private snapshots: Snapshot[] = []
  private readonly queuedActions: QueuedAction[] = []
  private readonly resyncTargets = new Map<string, SyncCursor>()
  private readonly resyncRunning = new Map<string, number>()
  private readonly resyncGenerations = new Map<string, number>()
  private readonly retryTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private readonly retryDelays = new Map<string, number>()
  private readonly reportedResyncErrors = new Set<string>()
  private reconcilePromise: Promise<void> | undefined
  private reconciling = false
  private disposed = false

  constructor(
    source: InstanceSyncSource,
    onChange: (snapshots: Snapshot[]) => void,
    onError: (error: Error) => void = () => undefined,
    onChatReset: (id: string) => void = () => undefined,
  ) {
    this.source = source
    this.onChange = onChange
    this.onError = onError
    this.onChatReset = onChatReset
  }

  get current(): Snapshot[] {
    return this.snapshots
  }

  /** Establishes an authoritative full baseline and replays events received during the request. */
  reconcile(): Promise<void> {
    if (this.disposed) return Promise.resolve()
    if (this.reconcilePromise) return this.reconcilePromise

    this.invalidateResyncs()
    this.reconciling = true
    const operation = (async () => {
      try {
        const snapshots = (await this.source.list()).map(normalizeSnapshot)
        if (this.disposed) return

        const previous = new Map(this.snapshots.map((snapshot) => [snapshot.server.id, snapshot]))
        const baseline = snapshots.map((snapshot) => {
          const old = previous.get(snapshot.server.id)
          return old && snapshot.chat.length === 0 ? { ...snapshot, chat: old.chat } : snapshot
        })
        this.commit(baseline)
      } catch (error) {
        if (!this.disposed) this.onError(asError(error))
      } finally {
        this.reconciling = false
        if (this.disposed) {
          this.queuedActions.length = 0
        } else {
          const actions = this.queuedActions.splice(0)
          for (const action of actions) action()
        }
      }
    })()
    this.reconcilePromise = operation
    void operation.then(() => {
      if (this.reconcilePromise === operation) this.reconcilePromise = undefined
    })
    return operation
  }

  /** Handles a WebSocket event in arrival order. */
  handle(event: Event): void {
    if (this.disposed) return
    if (this.reconciling) {
      this.queuedActions.push(() => this.processEvent(event))
      return
    }
    this.processEvent(event)
  }

  /** Applies a successful create response using the same ordering rules as a WebSocket snapshot. */
  acceptSnapshot(snapshot: Snapshot): void {
    const action = () => this.acceptSnapshotNow(normalizeSnapshot(snapshot))
    if (this.reconciling) this.queuedActions.push(action)
    else action()
  }

  /** Applies local changes, such as chat pagination, without bypassing the canonical snapshot store. */
  update(id: string, updater: SnapshotUpdater): void {
    const action = () => {
      const current = this.find(id)
      if (!current) return
      this.commit(
        this.snapshots.map((snapshot) => (snapshot.server.id === id ? updater(snapshot) : snapshot)),
      )
    }
    if (this.reconciling) this.queuedActions.push(action)
    else action()
  }

  dispose(): void {
    this.disposed = true
    for (const timer of this.retryTimers.values()) clearTimeout(timer)
    this.retryTimers.clear()
    this.queuedActions.length = 0
    this.invalidateResyncs()
  }

  private processEvent(event: Event): void {
    if (event.type === EVENT_INSTANCE_DELETED) {
      this.invalidateInstance(event.instanceId)
      this.commit(this.snapshots.filter((snapshot) => snapshot.server.id !== event.instanceId))
      return
    }

    if (event.type === EVENT_CHAT_RESET) {
      this.onChatReset(event.instanceId)
      this.update(event.instanceId, (snapshot) => ({ ...snapshot, chat: [] }))
      return
    }

    if (event.type === EVENT_CHAT_MESSAGE && event.data) {
      const message = event.data as ChatMessage
      this.update(event.instanceId, (snapshot) =>
        snapshot.chat.some((entry) => entry.id === message.id)
          ? snapshot
          : { ...snapshot, chat: [...snapshot.chat, message].slice(-MAX_CHAT_MESSAGES) },
      )
      return
    }

    if (event.type === EVENT_INSTANCE_UPDATED) {
      this.processPatch(event.instanceId, event.data)
      return
    }

    if (event.type === EVENT_INSTANCE_CREATED && isSnapshot(event.data)) {
      this.acceptSnapshotNow(normalizeSnapshot(event.data))
      return
    }

    if (isSnapshot(event.data)) this.acceptSnapshotNow(normalizeSnapshot(event.data))
  }

  private processPatch(id: string, value: unknown): void {
    const current = this.find(id)
    const patch = isPatch(value) ? value : undefined
    const target = patch ?? {
      revision: (current?.revision ?? 0) + 1,
      syncEpoch: current?.syncEpoch ?? '',
    }

    if (current && patch && isStaleInstancePatch(current, patch)) return
    if (this.resyncRunning.has(id)) {
      this.rememberTarget(id, target)
      return
    }
    if (!current || !patch) {
      this.requestResync(id, target)
      return
    }

    const next = applyInstancePatch(current, patch)
    if (next) {
      this.commit(this.snapshots.map((snapshot) => (sameInstance(snapshot, current) ? next : snapshot)))
      return
    }
    this.requestResync(id, target)
  }

  private acceptSnapshotNow(snapshot: Snapshot): void {
    this.commit(upsertSnapshot(this.snapshots, snapshot))
  }

  private requestResync(id: string, target: SyncCursor, preserveRetryDelay = false): void {
    if (this.disposed) return
    this.cancelRetry(id, !preserveRetryDelay)
    this.rememberTarget(id, target)
    const generation = this.generation(id)
    const runningGeneration = this.resyncRunning.get(id)
    if (runningGeneration === generation) return
    if (runningGeneration !== undefined) this.resyncRunning.delete(id)
    this.resyncRunning.set(id, generation)
    void this.runResync(id, generation)
  }

  private async runResync(id: string, generation: number): Promise<void> {
    try {
      while (!this.disposed && this.generation(id) === generation) {
        const target = this.resyncTargets.get(id)
        if (!target) return

        const snapshot = normalizeSnapshot(await this.source.get(id))
        if (this.disposed || this.generation(id) !== generation) return

        const latestTarget = this.resyncTargets.get(id)
        // If another patch arrived while GET was in flight, do not commit a
        // same-epoch snapshot that predates that patch. The next request gets
        // an authoritative snapshot at or after the latest target.
        if (
          latestTarget &&
          snapshot.syncEpoch === latestTarget.syncEpoch &&
          snapshot.revision < latestTarget.revision
        ) {
          await new Promise((resolve) => setTimeout(resolve, STALE_SNAPSHOT_RETRY_MS))
          continue
        }

        this.resyncTargets.delete(id)
        this.retryDelays.delete(id)
        this.reportedResyncErrors.delete(id)
        this.acceptSnapshotNow(snapshot)
      }
    } catch (error) {
      if (!this.disposed && this.generation(id) === generation) {
        if (!this.reportedResyncErrors.has(id)) {
          this.reportedResyncErrors.add(id)
          this.onError(asError(error))
        }
        this.scheduleRetry(id, generation)
      }
    } finally {
      if (this.resyncRunning.get(id) === generation) this.resyncRunning.delete(id)
    }
  }

  private rememberTarget(id: string, target: SyncCursor): void {
    const current = this.resyncTargets.get(id)
    if (newerCursor(target, current)) this.resyncTargets.set(id, target)
  }

  private scheduleRetry(id: string, generation: number): void {
    if (this.retryTimers.has(id) || !this.resyncTargets.has(id)) return
    const delay = this.retryDelays.get(id) ?? RETRY_INITIAL_MS
    this.retryDelays.set(id, Math.min(delay * 2, RETRY_MAX_MS))
    const timer = setTimeout(() => {
      this.retryTimers.delete(id)
      if (!this.disposed && this.generation(id) === generation) {
        const target = this.resyncTargets.get(id)
        if (target) this.requestResync(id, target, true)
      }
    }, delay)
    this.retryTimers.set(id, timer)
  }

  private cancelRetry(id: string, resetDelay = true): void {
    const timer = this.retryTimers.get(id)
    if (timer) clearTimeout(timer)
    this.retryTimers.delete(id)
    if (resetDelay) this.retryDelays.delete(id)
  }

  private invalidateInstance(id: string): void {
    this.resyncGenerations.set(id, this.generation(id) + 1)
    this.resyncTargets.delete(id)
    this.reportedResyncErrors.delete(id)
    this.cancelRetry(id)
  }

  private invalidateResyncs(): void {
    const ids = new Set([
      ...this.resyncTargets.keys(),
      ...this.resyncRunning.keys(),
      ...this.retryTimers.keys(),
    ])
    for (const id of ids) this.invalidateInstance(id)
  }

  private generation(id: string): number {
    return this.resyncGenerations.get(id) ?? 0
  }

  private find(id: string): Snapshot | undefined {
    return this.snapshots.find((snapshot) => snapshot.server.id === id)
  }

  private commit(snapshots: Snapshot[]): void {
    if (this.disposed) return
    this.snapshots = snapshots
    this.onChange(snapshots)
  }
}
