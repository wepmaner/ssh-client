.PHONY: lint test build dev bindings

lint:
	golangci-lint run ./...

test:
	go test ./...

build:
	wails3 build

dev:
	wails3 dev

bindings:
	wails3 generate bindings -clean=true -ts
