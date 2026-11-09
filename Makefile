PREFIX ?= $(HOME)/.local
VERSION ?= 0.1.0-dev

.PHONY: build check install
build:
	go build -trimpath -ldflags '-X main.version=$(VERSION)' -o dist/hycli ./cmd/hycli
check:
	@test -z "$$(gofmt -l cmd internal)"
	go test ./...
	go vet ./...
install: build
	install -d "$(PREFIX)/bin"
	install -m 755 dist/hycli "$(PREFIX)/bin/hycli"
