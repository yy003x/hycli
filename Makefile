PREFIX ?= $(HOME)/.local
VERSION ?= 0.2.0-dev

.PHONY: build test check fmt install release
build:
	go build -trimpath -ldflags '-X github.com/yy003x/hycli/internal/buildinfo.Version=$(VERSION)' -o build/hycli ./cmd/hycli
test:
	go test ./...
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race ./...
fmt:
	gofmt -w cmd internal
install: build
	PREFIX='$(PREFIX)' sh scripts/install.sh
release:
	VERSION='$(VERSION)' sh scripts/release.sh
