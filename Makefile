# tcp-split developer tasks.

GO ?= go
VERSION ?= dev

.PHONY: test vet build fmt-check

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Static, stripped, reproducible binary in dist/.
build:
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X github.com/mithro/go-tcp-split/internal/cli.Version=$(VERSION)" -o dist/tcp-split ./cmd/tcp-split

fmt-check:
	test -z "$$(gofmt -l .)"
