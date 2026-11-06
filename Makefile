# tcp-split developer tasks.

GO ?= go
VERSION ?= dev
V ?= 0.0.post1
A ?= $(shell $(GO) env GOARCH)

.PHONY: test vet build fmt-check deb test-netns test-netns-unpriv

test:
	sh packaging/check-units.sh
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Static, stripped, reproducible binary in dist/.
build:
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X github.com/mithro/go-tcp-split/internal/cli.Version=$(VERSION)" -o dist/tcp-split ./cmd/tcp-split

fmt-check:
	test -z "$$(gofmt -l .)"

# Debian package in dist/ (needs nfpm and a prior `make build`).
deb:
	VERSION=$(V) ARCH=$(A) nfpm package -p deb -f nfpm.yaml -t dist/

test-netns:
	sudo python3 test/netns/run.py --binary dist/tcp-split

test-netns-unpriv:
	unshare -Urnm -- python3 test/netns/run.py --binary dist/tcp-split
