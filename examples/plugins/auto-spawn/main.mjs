import { EVENT_CLIENT_APPEARANCE, EVENT_CLIENT_CHAT, log, on, start } from './sdk.mjs'

on(EVENT_CLIENT_APPEARANCE, async (event, instance) => {
  const snapshot = await instance.getSnapshot()
  if (snapshot.spawnReady && !snapshot.spawned) {
    await instance.requestSpawn()
    log('info', `spawn requested for ${event.instanceId}`)
  }
})

on(EVENT_CLIENT_CHAT, (event) => {
  log('info', `[${event.instanceId}] ${event.data.text ?? ''}`)
})

// The exported api object is useful from the browser debug console too:
// await api('instance.sendChat', instanceId, { text: 'hello' })
await start()
