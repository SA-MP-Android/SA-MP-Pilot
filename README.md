# SA-MP-Pilot

SA-MP-Pilot is a text-based SA-MP client with a browser console, multi-server instance management, and a programmable plugin system.

The backend is written in Go and implements SA-MP/RakNet communication. The frontend uses React, Vite, Tailwind CSS, and shadcn/ui.

## Features

| Area | Capabilities |
| --- | --- |
| Client | Multiple instances, auto-connect, chat, commands, player lists, Dialogs, TextDraws, vehicles, nearby entities, AFK, teleportation, and plugin-controlled straight-line movement |
| Protocol | Reliable RakNet transport, ordering channels, retransmission, split-packet reassembly, RPC, SA-MP authentication, and text encodings |
| Automation | JavaScript plugins, event subscriptions, instance APIs, hot reload, and a plugin debug console |
| Frontend | English, Simplified Chinese, and Russian; live WebSocket state; responsive layout |
| Distribution | Single-binary execution with the production frontend embedded in the executable |

Voice and audio streaming are intentionally outside the project scope.

## Requirements

| Tool | Version |
| --- | --- |
| Go | 1.26 or newer |
| Node.js | 22 or newer |
| pnpm | 10 or newer |

## Quick Start

### Development

```sh
pnpm --dir web install
make dev
```

The development servers listen on:

- Frontend: Vite's default port, `5173`
- Backend: `http://127.0.0.1:8080`

The frontend and backend can also be started separately:

```sh
pnpm --dir web dev
go run ./cmd/sa-mp-pilot -data . -web web/dist
```

### Build and run

```sh
make build
./bin/sa-mp-pilot
```

The build output is [`bin/sa-mp-pilot`](bin/sa-mp-pilot). The frontend is embedded, so the executable can be started from any working directory.

## Command-Line Options

| Option | Default | Description |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | HTTP listen address; non-loopback addresses are rejected because the API is unauthenticated |
| `-data` | Executable directory | Directory for data files, logs, and the default plugin directory |
| `-web` | Embedded assets | Use frontend assets from an external directory, useful during development |
| `-plugins` | `<data>/plugins` | Plugin directory |

At runtime, the data directory contains:

- `data.json`: server instances and quick commands
- `logs/`: per-instance runtime logs
- `plugins/`: the default plugin directory

## Plugin System

Plugins are separate child processes, with Node.js/JavaScript as the recommended language. They can subscribe to client events and use instance APIs for chat, Dialogs, AFK, vehicles, teleportation, straight-line walking/driving, instance management, and other automation tasks. Plugin event payloads use a stable camelCase JSON contract; plugin configuration and persistence remain owned by each plugin.

See [PLUGINS.md](PLUGINS.md) for the complete plugin documentation, including the manifest, events, APIs, debugging, and the wire protocol.

The repository includes an example plugin at [`examples/plugins/auto-spawn`](examples/plugins/auto-spawn):

```sh
./bin/sa-mp-pilot -plugins examples/plugins
```

Changes to `plugin.json` or source files automatically restart a running plugin. Unexpected plugin exits are automatically restarted by default with bounded backoff. New and removed plugin directories are detected while the application is running. Plugins are trusted local code; the current version does not provide a permission model or a plugin marketplace. The HTTP API, including plugin debugging and lifecycle control, is intentionally restricted to loopback addresses.

## Project Structure

```text
cmd/sa-mp-pilot/       Application entry point
internal/raknet/       RakNet transport layer
internal/samp/         SA-MP protocol, RPC, and events
internal/service/      Instance management, state, and APIs
internal/plugins/      Plugin process management and hot reload
plugin/                Public plugin protocol definitions
examples/plugins/      Example plugins
web/src/               React frontend
internal/webassets/    Embedded frontend assets
```

## Development Checks

```sh
make format
make lint
make test
make build
```

- `make lint` runs `go vet`, frontend linting, and TypeScript type checking
- `make test` runs the Go and frontend test suites
- `make build` creates a production binary with the frontend embedded

## Documentation

- [Plugin System](PLUGINS.md)
- [Frontend Development](web/README.md)
- [License](LICENSE)
- [NOTICE](NOTICE)

## License

Copyright 2026 SA-MP Android.

SA-MP-Pilot is licensed under the [Apache License 2.0](LICENSE).
