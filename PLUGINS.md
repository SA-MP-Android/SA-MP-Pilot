# SA-MP-Pilot Plugin System

The plugin system allows local JavaScript/Node.js processes to subscribe to SA-MP-Pilot events and perform automated operations through instance APIs.

Plugins run as separate processes. The host and plugin communicate through JSON Lines on stdin/stdout. Python, Go, or any other language that can read and write JSON Lines can also be used to implement a plugin. Plugin stdout is reserved for protocol messages; JavaScript SDK `console` methods are redirected to plugin logs.

The host applies bounded resource policies: each plugin has a 256-message outbound queue and may drop high-frequency events when it cannot keep up; plugin logs retain the most recent 500 entries and truncate each entry at 16 KiB; protocol messages are limited to 1 MiB; subscriptions are limited to 256 entries of 256 bytes each; and each plugin may have at most 32 concurrent API calls. The plugin list reports `eventsDropped` so automation can detect overload.

## Quick Start

### Directory Layout

The default plugin directory is `<data>/plugins`. Each direct child directory is treated as a plugin:

```text
plugins/
└── my-plugin/
    ├── plugin.json
    ├── main.mjs
    └── sdk.mjs
```

The directory can be overridden from the command line:

```sh
./bin/sa-mp-pilot -plugins /path/to/plugins
```

To run the repository example:

```sh
./bin/sa-mp-pilot -plugins examples/plugins
```

### Minimal Manifest

`plugin.json` requires at least `id` and `command`:

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "description": "A simple SA-MP-Pilot plugin.",
  "command": "node",
  "args": ["main.mjs"],
  "events": ["client.chat"],
  "enabled": true
}
```

| Field | Type | Description |
| --- | --- | --- |
| `id` | `string` | Required, path-safe, and unique |
| `name` | `string` | Display name |
| `version` | `string` | Plugin version |
| `description` | `string` | Plugin description |
| `command` | `string` | Startup command, such as `node` |
| `args` | `string[]` | Arguments passed to the startup command |
| `events` | `string[]` | Initial event subscriptions; omitted or empty means all events |
| `enabled` | `boolean` | Set to `false` to prevent startup |

The host starts the process with the plugin directory as its working directory. A relative command containing a path separator is resolved relative to the plugin directory.

## JavaScript SDK

You can copy [`examples/plugins/auto-spawn/sdk.mjs`](examples/plugins/auto-spawn/sdk.mjs) as a starting point. A minimal plugin looks like this:

```js
import { EVENT_CHAT, log, on, start } from './sdk.mjs'

on(EVENT_CHAT, (event, instance) => {
  log('info', `[${event.instanceId}] ${event.data.Text ?? ''}`)
})

await start()
```

The SDK exports:

| Export | Purpose |
| --- | --- |
| `on(pattern, handler)` | Register an event handler and return an unsubscribe function |
| `start()` | Send `ready` to the host and start processing messages |
| `log(level, message)` | Write a plugin log entry |
| `api(method, instanceId, params)` | Call a low-level API method |
| `instanceApi(instanceId)` | Create a convenience API for one instance |
| `dispatch(event)` | Dispatch a test event to the plugin's registered handlers |

Event handlers receive objects with this shape:

```js
{
  name: 'client.dialog',
  instanceId: 'instance-id',
  time: '2026-01-01T00:00:00.000Z',
  data: { /* event data */ },
}
```

`instanceApi(instanceId)` can be used as follows:

```js
import { instanceApi } from './sdk.mjs'

const instance = instanceApi(event.instanceId)

await instance.getSnapshot()
await instance.sendChat('hello')
await instance.sendCommand('/help')
await instance.setAFK(true)
```

### Subscription Matching

Subscriptions support exact matching, prefix matching, and a catch-all pattern:

| Subscription | Matches |
| --- | --- |
| `client.dialog` | Only `client.dialog` |
| `client.*` | Every event beginning with `client.` |
| `*` | Every event |

Errors thrown by event handlers are written to the plugin log and do not block the host's network loop.

## Events

### Host Events

| Event | Description |
| --- | --- |
| `instance.created` | A new instance was created |
| `instance.updated` | An instance snapshot changed |
| `instance.deleted` | An instance was deleted |
| `chat.message` | A chat history entry was added |
| `chat.reset` | Chat history was reset |

### Client Events

Client events use the `client.` prefix. The currently available events are:

| Event | `data` contents |
| --- | --- |
| `client.joined` | Local player ID |
| `client.chat` | Chat message: `Text`, `Color`, and optional `PlayerID` |
| `client.player.join` | Player ID, name, and color |
| `client.player.quit` | Player ID |
| `client.scores` | Player score and ping list |
| `client.dialog` | Dialog: `ID`, `Style`, `Title`, `Message`, `Button1`, and `Button2` |
| `client.disconnected` | Disconnect reason |
| `client.protocol.error` | Protocol error information |
| `client.textdraw.show` | Complete TextDraw data |
| `client.textdraw.hide` | TextDraw ID |
| `client.textdraw.text` | TextDraw ID and text |
| `client.object.add` | Object ID, model, and coordinates |
| `client.object.remove` | Object ID |
| `client.vehicle.add` | Vehicle ID, model, and coordinates |
| `client.vehicle.remove` | Vehicle ID |
| `client.player.sync` | Player synchronization data |
| `client.position` | `[x, y, z]` |
| `client.appearance` | Player appearance or spawn data |
| `client.vehicle.state` | Vehicle and passenger state |
| `client.spawned` | The local player has spawned |
| `client.vehicle.sync` | Vehicle synchronization data |

Raw client event data comes from Go protocol structures, so field names are generally preserved with an uppercase first letter. For example, a Dialog title is `event.data.Title`. Instance snapshots use the camelCase JSON API fields, such as `snapshot.activeDialog` and `snapshot.players`.

Dialog automation example:

```js
import { api, on, start } from './sdk.mjs'

on('client.dialog', async (event) => {
  if (event.data.Title !== 'Login Window') return

  await api('instance.respondDialog', event.instanceId, {
    dialogId: event.data.ID,
    buttonId: 1,
    listItem: 0,
    inputText: 'password',
  })
})

await start()
```

## Instance APIs

`instanceApi(instanceId)` provides the following methods:

| Method | Parameters | Purpose |
| --- | --- | --- |
| `getSnapshot()` | None | Get the complete instance snapshot |
| `getChat(options?)` | `before`, `limit` | Get a page of chat history |
| `sendChat(text)` | `text` | Send chat text; strings beginning with `/` are sent as commands |
| `sendCommand(command)` | `command` | Send a server command; `/` is added automatically |
| `requestSpawn()` | None | Request a spawn |
| `refreshScores()` | None | Request a player score refresh |
| `setKeys(mask)` | `mask` | Send a key mask |
| `setAFK(enabled)` | `enabled` | Enable or disable AFK |
| `teleport(x, y, z)` | Coordinates | Set the position and send synchronization |
| `enterVehicle(vehicleId, passenger?)` | Vehicle ID, passenger flag | Enter a vehicle |
| `exitVehicle()` | None | Exit a vehicle |
| `respondDialog(dialogId, buttonId, listItem?, inputText?)` | Dialog parameters | Send a Dialog response |
| `clickPlayer(playerId)` | Player ID | Click a player |
| `clickTextDraw(textDrawId)` | TextDraw ID | Click a TextDraw |
| `call(method, params?)` | Low-level method and parameters | Call any plugin API method |

The top-level `api(method, instanceId, params)` function also supports:

| Method | Purpose |
| --- | --- |
| `instances.list` | Get all instance snapshots |
| `instance.getSnapshot` | Get a specific instance snapshot |
| `instance.getChat` | Get a chat history page |
| `instance.connect` | Connect an instance |
| `instance.disconnect` | Disconnect an instance |
| `instance.sendChat` | Send chat |
| `instance.sendCommand` | Send a command |
| `instance.requestSpawn` | Request a spawn |
| `instance.refreshScores` | Refresh scores |
| `instance.setKeys` | Send a key mask |
| `instance.setAFK` | Set AFK |
| `instance.teleport` | Teleport |
| `instance.enterVehicle` | Enter a vehicle |
| `instance.exitVehicle` | Exit a vehicle |
| `instance.respondDialog` | Respond to a Dialog |
| `instance.clickPlayer` | Click a player |
| `instance.clickTextDraw` | Click a TextDraw |
| `instance.action` | Call the generic action extension point |

### Snapshot Example

```js
const snapshot = await api.getSnapshot()

return {
  status: snapshot.connection.status,
  players: snapshot.players.length,
  position: snapshot.players.find((player) => player.id === 0),
}
```

## Browser Debug Console

The global Debug Console is separate from the plugin manager. It runs temporary code inside a selected running plugin process, so the selected plugin is the execution context rather than a plugin being edited inline.

Debug execution has a 5-second synchronous VM limit and a 15-second asynchronous request limit. Debug source is limited to 256 KiB. API calls made by JavaScript plugins expire after 15 seconds if the host does not answer. A debug request is rejected while its plugin is still `starting`; wait for the `running` status after the `ready` handshake.

- `Run`: execute the current JavaScript
- Plugin manager: `Start`, `Stop`, and `Restart` control the plugin lifecycle
- Logs: inspect plugin stdout/stderr and host errors
- Live events: inspect events sent to the plugin

Debug code can interact with that plugin context in four ways:

- `api` and `instanceApi()`: call SA-MP-Pilot instance APIs
- `on()`: register an event handler; subscriptions are sent to the host immediately and remain active until the returned unsubscribe function is called or the plugin restarts
- `dispatch()`: manually trigger registered handlers with a test event
- `state`: a persistent object shared by debug evaluations in that plugin process

The console does not expose the lexical variables inside `main.mjs`. Use `state` for temporary debug state, or explicitly expose application state from the plugin when needed.

The debug console is intended for trusted local users. The executable refuses non-loopback HTTP listen addresses because plugin debug and lifecycle endpoints are not remotely authenticated.

Debug code can use the current instance's `api` object:

```js
const snapshot = await api.getSnapshot()
return snapshot.players.map((player) => player.name)
```

For example, this registers a debug handler and tests it without waiting for a server event:

```js
state.lastMessage = null
on('client.chat', (event) => {
  state.lastMessage = event.data
})

await dispatch({
  name: 'client.chat',
  instanceId: 'instance-id',
  data: { Text: 'debug message' },
})

return state.lastMessage
```

Debug code is not saved as plugin source. Plugin source files and the manifest must still be maintained in the plugin directory.

## Lifecycle and Hot Reload

At startup, the host scans the plugin directory and starts plugins whose `enabled` field is not `false`. New and removed plugin directories are detected while the application is running. Changes to plugin files automatically restart the corresponding plugin. Changes under `node_modules/` do not trigger hot reloads. A plugin must send the `ready` message during startup; otherwise it is stopped after the handshake timeout.

Plugin statuses are:

| Status | Description |
| --- | --- |
| `starting` | The process started and the host is waiting for its `ready` handshake |
| `running` | The plugin process is running |
| `stopped` | The plugin has stopped |
| `disabled` | The manifest disabled the plugin |
| `error` | Startup or runtime failure |

The HTTP API also provides lifecycle endpoints:

```text
GET  /api/plugins
POST /api/plugins/{id}/debug
POST /api/plugins/{id}/start
POST /api/plugins/{id}/stop
POST /api/plugins/{id}/restart
```

## JSON Lines Protocol

Language runtimes can implement the protocol defined in [`plugin/protocol.go`](plugin/protocol.go) directly.

The host and plugin exchange one JSON object per line. The current protocol version is `1`. Common message types include:

| Type | Direction | Description |
| --- | --- | --- |
| `hello` | Host → plugin | Protocol version and plugin ID |
| `ready` | Plugin → host | Plugin startup completion and subscriptions |
| `event` | Host → plugin | A subscribed event |
| `call` | Plugin → host | An instance API request |
| `result` | Host → plugin | API result |
| `debug` | Host → plugin | Execute debug code |
| `debug.result` | Plugin → host | Debug result |
| `log` | Plugin → host | Plugin log entry |
| `subscribe` | Plugin → host | Update event subscriptions |

Protocol data and parameters remain open-ended. New client capabilities can be exposed through `instance.action` without changing the overall host and SDK structure immediately.

## Security Boundary

Plugins have the same local process permissions as the user running SA-MP-Pilot. They can read and write files, access the network, and start other processes. The current version provides:

- No permission sandbox
- No plugin signature verification
- No plugin marketplace
- No resource isolation between plugins

The JavaScript `vm` context is an execution context, not a security boundary. Debug code must therefore be treated as trusted local code, just like the plugin itself.

Only install and run plugins you trust.
