# tcp-split

`tcp-split` is a split-TCP proxy for lossy VPN clients. A laptop reaches its home network and a remote VPN server through WireGuard tunnels; over a long, lossy path the tunnel legs can see double-digit packet loss and several hundred milliseconds of round-trip time, and a forwarded TCP connection runs the far server's congestion control over that whole lossy path. `tcp-split` makes every forwarded TCP connection terminate on the VPN server (which runs BBR): the server intercepts the connection, opens its own connection to the real destination, and relays bytes both ways, so the lossy leg is always a server-side BBR socket and the clean leg runs over a path with no loss. It is a performance-enhancing proxy, not a privacy, circumvention or security feature.

## Status

Under construction.

## Build

```
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/mithro/go-tcp-split/internal/cli.Version=$(VERSION)" -o dist/tcp-split ./cmd/tcp-split
```

Or `make build` (`VERSION` defaults to `dev`).
