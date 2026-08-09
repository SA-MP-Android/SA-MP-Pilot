.PHONY: dev build test lint format
dev:
	pnpm --dir web dev & go run ./cmd/sa-mp-pilot -data . -web web/dist
build:
	pnpm --dir web build
	rm -rf internal/webassets/dist
	cp -R web/dist internal/webassets/dist
	go build -tags release -o bin/sa-mp-pilot ./cmd/sa-mp-pilot
test:
	go test ./...
	pnpm --dir web test
lint:
	go vet ./...
	pnpm --dir web lint
format:
	gofmt -w cmd internal
	pnpm --dir web format
