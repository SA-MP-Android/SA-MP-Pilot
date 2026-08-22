import {
  EVENT_CLIENT_APPEARANCE,
  EVENT_CLIENT_CHAT,
  log,
  on,
  start,
} from './sdk.mjs'

const pendingRequests = new Map()

async function requestSpawn(event, instance) {
  if (pendingRequests.has(event.instanceId)) return pendingRequests.get(event.instanceId)
  const request = (async () => {
    const snapshot = await instance.getSnapshot()
    if (snapshot.spawnReady && !snapshot.spawned) {
      await instance.requestSpawn()
      log('info', `spawn requested for ${event.instanceId}`)
    }
  })()
  pendingRequests.set(event.instanceId, request)
  try {
    await request
  } finally {
    if (pendingRequests.get(event.instanceId) === request) pendingRequests.delete(event.instanceId)
  }
}

// Respawn is owned by the built-in client lifecycle policy. Keeping this
// example focused on the initial appearance avoids a plugin request bypassing
// the client's death cooldown or racing its automatic worker.
on(EVENT_CLIENT_APPEARANCE, requestSpawn)

on(EVENT_CLIENT_CHAT, (event) => {
  log('info', `[${event.instanceId}] ${event.data.text ?? ''}`)
})

// The exported api object is useful from the browser debug console too:
// await api('instance.sendChat', instanceId, { text: 'hello' })
await start()
