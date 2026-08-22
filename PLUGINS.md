# SA-MP-Pilot Plugin System

SA-MP-Pilot plugins are trusted local programs that run as separate child processes. The host and plugin communicate through one JSON object per line on stdin/stdout. Node.js/JavaScript is the recommended runtime, but any language can implement the same protocol.

The public plugin contract is defined by [`plugin/protocol.go`](plugin/protocol.go) and the JavaScript reference SDK at [`examples/plugins/auto-spawn/sdk.mjs`](examples/plugins/auto-spawn/sdk.mjs). Plugin-facing JSON uses camelCase consistently. Internal Go and SA-MP decoder field names are never part of the plugin contract.

## Quick start

The default plugin directory is `<data>/plugins`. Every direct child directory containing `plugin.json` is a plugin:

```text
plugins/
└── my-plugin/
    ├── plugin.json
    ├── main.mjs
    └── sdk.mjs
```

Run the application with a custom plugin directory using `-plugins /path/to/plugins`. The repository example can be run with `-plugins examples/plugins`.

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "description": "A simple SA-MP-Pilot plugin.",
  "command": "node",
  "args": ["main.mjs"],
  "events": ["client.chat"],
  "enabled": true,
  "restart": true
}
```

Manifest fields:

| Field | Type | Description |
| --- | --- | --- |
| `id` | string | Required, unique, path-safe plugin identifier |
| `name` | string | Display name |
| `version` | string | Plugin version chosen by the author |
| `description` | string | Human-readable description |
| `command` | string | Executable, for example `node`, `python`, or an absolute path |
| `args` | string[] | Arguments passed to the executable |
| `events` | string[] | Initial subscriptions; omitted or empty means `*` until the plugin sends its ready subscription set |
| `enabled` | boolean | `false` prevents startup |
| `restart` | boolean | Unexpected exits are automatically restarted with exponential backoff; defaults to `true` |

The process working directory is the plugin directory. A relative command containing a path separator is resolved relative to that directory. Plugin configuration, files, databases, and persistent state belong to the plugin and may be managed by its own language/runtime.

## JavaScript SDK

Copy the reference SDK into a plugin directory. It is intentionally a plain `.mjs` file and does not require npm installation.

```js
import { EVENT_CLIENT_CHAT, instanceApi, log, on, start } from './sdk.mjs'

on(EVENT_CLIENT_CHAT, (event, instance) => {
  log('info', `[${event.instanceId}] ${event.data.text}`)
  void instance.sendChat('hello')
})

await start()
```

Exports:

| Export | Purpose |
| --- | --- |
| `on(pattern, handler)` | Register a handler and return an unsubscribe function |
| `dispatch(event)` | Dispatch a synthetic event to matching handlers |
| `start()` | Send the ready handshake and process host messages; may only be called once |
| `log(level, message)` | Send a bounded log entry to the host |
| `api(method, instanceId, params)` | Low-level API call |
| `instanceApi(instanceId)` | Typed convenience methods for one instance |
| `instancesApi()` | Instance listing, creation, update, and deletion methods |
| `MAX_SUBSCRIPTIONS`, `MAX_SUBSCRIPTION_BYTES` | Subscription limits enforced by the host and SDK |
| `LOG_LEVEL_INFO`, `LOG_LEVEL_WARN`, `LOG_LEVEL_ERROR` | Standard log-level constants |
| `EVENTS` | Frozen map of all supported event names |
| `METHOD` | Frozen map of all supported API method names |

`on()` accepts exact names, suffix wildcards such as `client.*`, and `*`. Event handler failures are isolated and written to the plugin log. Event handlers may run concurrently; plugins that require ordering must implement their own queue.

The SDK validates event patterns, bounds log messages to 16 KiB, rejects non-JSON API parameters immediately, and always attempts to return a debug error instead of leaving the host request waiting for a timeout.

## Event envelope

Every event delivered to a plugin has this shape:

```js
{
  name: 'client.dialog',
  instanceId: 'instance-id',
  time: '2026-01-01T00:00:00.000Z',
  data: { /* event-specific camelCase data */ }
}
```

`instanceId` is omitted for global events. Host events use the following data:

| Event | `data` |
| --- | --- |
| `instance.created` | Instance snapshot; chat history is delivered separately through `chat.message`/`instance.getChat` |
| `instance.updated` | Incremental patch: `{ revision, syncEpoch, operations }` |
| `instance.deleted` | Omitted |
| `chat.message` | `{ id, text, color, at }` |
| `chat.reset` | Omitted |

`instance.updated` operations currently replace top-level snapshot paths. A plugin should use `instance.getSnapshot()` as its recovery source when it misses events or does not want to implement patch application.

Snapshots expose the local player's state as `localPlayer: { id, health, armour, lifeState }`. `lifeState` is one of `class_selection`, `spawn_ready`, `spawn_request_pending`, `spawned`, `dead`, or `disconnected`. When the client is driving or riding, `vehicleState` also includes the current vehicle's `health` and `healthKnown`; only use `health` when `healthKnown` is true. The matching entry in `vehicles` remains the complete vehicle record.

## Client events

All client payloads use camelCase. IDs inside entity payloads are named `id`; `playerId` is used only where the value specifically identifies a player in another object.

| Event | Data |
| --- | --- |
| `client.joined` | `{ playerId }` |
| `client.chat` | `{ text, color?, playerId? }` |
| `client.player.join` | `{ id, name, color? }` |
| `client.player.quit` | `{ id }` |
| `client.scores` | `[{ id, score, ping }]` |
| `client.dialog` | `{ id, style, title, message, button1, button2 }`; a negative `id` indicates the server closed the active Dialog |
| `client.disconnected` | `{ reason }` |
| `client.protocol.error` | `{ message }` |
| `client.textdraw.show` | Complete TextDraw data: `id`, `text`, `style`, `flags`, `shadow`, `outline`, `selectable`, `letterColor`, `boxColor`, `backgroundColor`, `x`, `y`, `letterWidth`, `letterHeight`, `lineWidth`, `lineHeight`, `modelId` |
| `client.textdraw.hide` | `{ id }` |
| `client.textdraw.text` | `{ id, text }` |
| `client.object.add` | `{ id, modelId, x, y, z }` |
| `client.object.remove` | `{ id }` |
| `client.vehicle.add` | `{ id, modelId, x, y, z, health }` |
| `client.vehicle.remove` | `{ id }` |
| `client.player.sync` | `{ id, x, y, z, health, armour, skin, team, rotation, color? }` |
| `client.position` | `{ x, y, z }` |
| `client.appearance` | `{ id, x?, y?, z?, skin?, team?, rotation?, color? }`; only fields present in the server update are included |
| `client.player.health` | `{ health, armour }`; emitted when the server updates the local player's health or armour, and when a successful spawn resets the local state to the default values |
| `client.player.state` | `{ state }`; emitted for lifecycle transitions such as `spawn_request_pending`, `spawn_ready`, and `spawned` |
| `client.player.death` | `{ reason, killerId, reasonKnown, source }`; emitted once when a spawned local player enters the dead state. `reason` is `255` and `reasonKnown` is `false` when the authoritative update does not contain weapon metadata; `killerId` is `-1` when unknown. `source` is `server_health`, `vehicle`, or `rc_vehicle` depending on the death authority |
| `client.vehicle.state` | `{ inVehicle, passenger, vehicleId, health, healthKnown }`; `vehicleId` is `-1` when not in a vehicle and `healthKnown` is false when the current vehicle's health is not known yet |
| `client.vehicle.health` | `{ id, health }`; emitted for `SetVehicleHealth` and vehicle-death updates |
| `client.spawned` | `{}` |
| `client.vehicle.sync` | `{ id, modelId, x, y, z, health }` |
| `client.movement.started` | `{ taskId, kind, state, x, y, z, targetX, targetY, targetZ, progress }` |
| `client.movement.progress` | Same movement payload, emitted at a throttled progress cadence |
| `client.movement.completed` | Same movement payload with `progress: 1` |
| `client.movement.stopped` | Same movement payload; emitted when a task is cancelled or replaced |
| `client.movement.failed` | Same movement payload with a non-empty `error` |

Color values are eight-digit strings such as `#11223344`. Chat color is optional when the server packet does not provide one. Dialog event data intentionally excludes the internal raw encoded message; the host uses that data internally when responding to list dialogs.

## API

### One instance

```js
const instance = instanceApi(instanceId)

await instance.getSnapshot()
await instance.getChat({ before: 0, limit: 50 })
await instance.connect()
await instance.disconnect()
await instance.sendChat('hello')
await instance.sendCommand('/help')
await instance.requestSpawn()
await instance.refreshScores()
await instance.setKeys(0)
await instance.setAFK(true)
await instance.teleport(1, 2, 3)
const vehicle = (await instance.getSnapshot()).vehicles.find((item) => item.distance <= 4.5)
if (!vehicle) throw new Error('no nearby vehicle')
await instance.enterVehicle(vehicle.id, false, 'normal')
// Observe client.vehicle.state with inVehicle: true before treating entry as confirmed.
const drive = await instance.driveTo(100, 200, 5, { vehicleId: vehicle.id, speed: 12, tolerance: 1 })
await instance.exitVehicle()
const walk = await instance.walkTo(10, 20, 3, { speed: 1.4, tolerance: 0.35 })
await instance.stopMovement()
await instance.respondDialog(12, 1, 0, 'password')
await instance.clickPlayer(42)
await instance.clickTextDraw(7)
await instance.addCommand({ label: 'Help', command: '/help' })
await instance.deleteCommand(commandId)
```

`walkTo` and `driveTo` return a task ID immediately. They run one straight-line movement task per instance; starting a new task replaces the current one. `driveTo` requests the driver seat through the normal `instance.enterVehicle` RPC and mirrors the normal client's in-car state after that request is sent; a server-side correction can still end the task. It is rejected immediately if the client is currently a passenger. The task ends when the target tolerance is reached, or when `stopMovement`, a server position correction, AFK, or a disconnect interrupts it. These helpers do not perform collision or path finding, so they require no game assets or map downloads.

`enterVehicle(vehicleId, passenger = false, mode = 'direct')` accepts two entry modes. `direct` is the backward-compatible default: it sends `RPC_EnterVehicle` and starts vehicle/passenger sync immediately. `normal` follows the network-visible sequence of the regular client: it sends the same `RPC_EnterVehicle` at entry start, walks toward the streamed vehicle with on-foot sync when necessary, keeps that sync during the entry phase, then switches to vehicle/passenger sync. It requires the streamed vehicle to be within the normal 8-meter entry range. It does not fabricate the server-to-client `PutPlayerInVehicle` RPC; that RPC is reserved for server-forced placement. The headless client has no GTA animation task, so `normal` models the task's observable RPC/sync timing. The method resolves after its transition phase; for both modes, `client.vehicle.state` is the event to observe before issuing a dependent action.

Vehicle IDs are server entity IDs from `snapshot.vehicles[].id`, not GTA model IDs such as `411`. The vehicle should be streamed and within normal entry range before calling `enterVehicle` or `driveTo`.

`instance.call(method, params)` is available for forward-compatible or generic calls. `instance.action(action, params)` supports the current UI actions `chat`, `spawn`, `keys`, `afk`, `teleport`, `enterVehicle`, `exitVehicle`, `walkTo`, `driveTo`, `stopMovement`, `dialog`, `textDraw`, `clickPlayer`, `deferDialog`, `showDialog`, and `dismissDialog`. The application uses an explicit automatic-respawn policy for connected instances; plugins can react to `client.player.death` or `client.player.state` and call `instance.requestSpawn()` when they need to request or coordinate a spawn. A death-triggered spawn request is subject to the client's 2.5-second respawn cooldown.

### Instance management

```js
const instances = instancesApi()
const all = await instances.list()
const created = await instances.create({ host: '127.0.0.1', port: 7777, nickname: 'Pilot', password: '' })
await instances.update(created.server.id, { host: '127.0.0.1', port: 7777, nickname: 'Pilot', password: '' })
await instances.delete(created.server.id)
```

Low-level method names are:

| Method | Purpose |
| --- | --- |
| `instances.list` | List snapshots |
| `instances.create` | Create an instance from a server configuration |
| `instances.update` | Update the instance server configuration |
| `instances.delete` | Delete an instance |
| `instance.getSnapshot` | Get one snapshot |
| `instance.getChat` | Get paginated chat history |
| `instance.connect` / `instance.disconnect` | Control connection lifecycle |
| `instance.sendChat` / `instance.sendCommand` | Send chat or a server command |
| `instance.requestSpawn` | Request a spawn |
| `instance.refreshScores` | Request score refresh |
| `instance.setKeys` / `instance.setAFK` | Control input state |
| `instance.teleport` | Teleport and synchronize |
| `instance.enterVehicle` / `instance.exitVehicle` | Vehicle control; `enterVehicle` supports `direct` and `normal` modes |
| `instance.walkTo` / `instance.driveTo` / `instance.stopMovement` | Start or cancel plugin-controlled straight-line movement |
| `instance.respondDialog` | Respond to a Dialog |
| `instance.clickPlayer` / `instance.clickTextDraw` | Click UI targets |
| `instance.commands.add` / `instance.commands.delete` | Manage Quick Commands |
| `instance.action` | Generic action extension point |

Missing or invalid typed parameters return an API error. Numeric values must be finite integers where an ID/mask is expected; coordinates must be finite numbers. Errors are returned as strings in the protocol `error` field and rejected as JavaScript `Error` objects by the SDK.

## Lifecycle and hot reload

The host scans the plugin directory at startup and watches it once per second. New valid plugin directories are started, removed directories are stopped and removed, and changes to plugin files restart the plugin. `node_modules/` is excluded from file-change detection.

Statuses:

| Status | Meaning |
| --- | --- |
| `starting` | Process started and waiting for `ready` |
| `running` | Ready handshake completed |
| `stopped` | Intentionally stopped or exited with auto-restart disabled |
| `disabled` | Manifest has `enabled: false` |
| `error` | Startup, protocol, or runtime failure |

Unexpected exits are restarted by default after 1, 2, 4 seconds and so on, capped at 30 seconds. A stable plugin resets its backoff after the ready handshake. Set `restart` to `false` when a plugin should remain stopped after a crash.

The HTTP API provides:

```text
GET  /api/plugins
POST /api/plugins/{id}/debug
POST /api/plugins/{id}/start
POST /api/plugins/{id}/stop
POST /api/plugins/{id}/restart
```

## Browser Debug Console

The browser Debug Console executes temporary JavaScript inside a selected running plugin process. It is not a source editor: changes are not saved to the plugin directory. Use `state` for values that should survive between debug evaluations in the same process.

Debug code can use `api`, `instanceApi()`, `instancesApi()`, `on()`, `dispatch()`, `log()`, and `console`. For example:

```js
const snapshot = await api.getSnapshot()
return snapshot.players.map((player) => player.name)
```

The host limits debug source to 256 KiB, synchronous execution to 5 seconds, and asynchronous execution to 15 seconds. The plugin must already be `running`; calls made while it is `starting` are rejected. Debug code has the same trusted-process permissions as the plugin and the JavaScript VM is not a security boundary.

## JSON Lines protocol

The current pre-release protocol version is `1`. This version defines canonical camelCase client payloads and explicit empty-subscription semantics. A plugin that reports an incompatible version is rejected during the ready handshake.

| Type | Direction | Purpose |
| --- | --- | --- |
| `hello` | Host → plugin | Protocol version and plugin ID |
| `ready` | Plugin → host | Startup completion and initial subscriptions |
| `event` | Host → plugin | A matching event |
| `call` | Plugin → host | API request with correlation ID |
| `result` | Host → plugin | API response |
| `subscribe` | Plugin → host | Replace runtime subscriptions |
| `log` | Plugin → host | Structured plugin log |
| `debug` | Host → plugin | Execute trusted debug code |
| `debug.result` | Plugin → host | Debug result or error |

An explicit empty `events: []` in `ready` or `subscribe` means no subscriptions. An omitted or `null` `events` field means “do not change the manifest/default subscription set” for `ready`; `subscribe` must include an `events` array containing the complete replacement set. Subscription patterns are exact names, suffix wildcards such as `client.*`, or `*`.

The host limits each plugin to a 256-message outbound queue, 1 MiB protocol messages, 256 subscriptions of at most 256 bytes each, 500 retained log entries, 16 KiB per log entry, and 32 concurrent API calls. High-frequency events may be dropped when a plugin cannot keep up; `eventsDropped` is exposed by `GET /api/plugins`. Event delivery is not replayable, so plugins should bootstrap from `instances.list`/`instance.getSnapshot` and recover from a dropped-event gap with a fresh snapshot.

## Security boundary

Plugins run with the same operating-system permissions as SA-MP-Pilot. They can read/write files, access the network, and start other processes. There is currently no sandbox, signature verification, marketplace, or per-plugin permission isolation. The browser debug console executes code inside the selected plugin process and is only suitable for trusted local users. The executable refuses non-loopback HTTP listen addresses because these endpoints are not remotely authenticated.

Only install and run plugins you trust.
