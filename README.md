# SA-MP-Pilot

SA-MP-Pilot is a browser console for managing multiple SA-MP server sessions. The backend is written in Go; the frontend uses React, Vite, Tailwind CSS, and shadcn/ui components.

The interface is available in English, Simplified Chinese, and Russian. Language resources live under `web/src/i18n/locales`, while feature components are grouped under `web/src/features` and reusable UI building blocks under `web/src/components`.

## Requirements

- Go 1.26 or newer
- Node.js 22 or newer
- pnpm 10 or newer

## Development

```sh
pnpm --dir web install
pnpm --dir web dev
go run ./cmd/sa-mp-pilot -data . -web web/dist
```

Vite proxies `/api` to `127.0.0.1:8080`. By default, the Go service stores `data.json` beside the executable and writes isolated instance logs under the adjacent `logs` directory. Starting a new connection clears that instance's log. Override the bind address and data directory with `-addr` and `-data`.

Release archives include the platform binary and the pre-built frontend. Extract the complete archive and run the binary from any working directory; it resolves the bundled `web/dist` directory relative to the executable. Tags beginning with `v` trigger cross-platform release builds and SHA-256 checksums.

## Verification

```sh
make format
make lint
make test
make build
```

The production build is written to `bin/sa-mp-pilot`; it serves the pre-built frontend from `web/dist`.

## Protocol and features

The backend contains a pure-Go implementation of the legacy RakNet wire protocol used by SA-MP 0.3.7. It supports the cookie and connection handshakes, reliability and ordering channels, acknowledgements, retransmission, split-packet reassembly, RPC framing, SA-MP authentication, text compression and configurable UTF-8/GBK/Windows-1251 text encoding.

The browser console supports multiple persisted instances, automatic connection, server configuration, chat and commands, reusable quick commands, player list and player clicks, Dialog responses and deferral, TextDraw display and clicks, nearby player/vehicle/object data, key masks, AFK mode, teleportation, and driver/passenger vehicle controls. Voice and audio streaming are intentionally excluded.

Network input is decoded field-by-field rather than mapped to platform-native packed structures. Queues, reliable retries, ordered frames, split assemblies and payload sizes are bounded; connection work is cancellation-aware and uses one transport event loop per session.

## License

Copyright 2026 SA-MP Android.

SA-MP-Pilot is licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
