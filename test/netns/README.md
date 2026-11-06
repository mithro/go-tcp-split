# Network-namespace end-to-end harness

`run.py` (Python 3, stdlib only) proves that the real nft example plus the real
binary intercept IPv4 and IPv6 TCP end to end.

```
 client ns                server ns                         internet ns
 veth-c0 ----------------- veth-c     veth-i ------------- veth-i0
 10.99.0.2/24              10.99.0.1/24 10.99.1.1/24        10.99.1.2/24
 fd99:0::2/64              fd99:0::1/64 fd99:1::1/64        fd99:1::2/64
                           nft table inet tcp_split         echo servers :8080
                           tcp-split --listen :10443        (v4 and v6)
                           veth-l ------------------------- veth-l0   lan ns
                           10.99.3.1/24                     10.99.3.9/24
                                                            echo server :8080
```

`lan` is a network behind the server that is not the WAN (`veth-i`), standing
in for anything the server routes elsewhere (another tunnel, a LAN).

The harness prepends `define VPN_IFS = { "veth-c" }`, `define WAN_IF = "veth-i"`
and `define PORT = 10443` to `packaging/examples/redirect.nft` (the shipped file
is never edited) and appends a separate `inet tcp_split_test` table with an
`oifname $WAN_IF masquerade` postrouting chain. The server namespace also routes
`10.99.2.0/24` towards the internet namespace (the client routes it via the
server), so the `denied` destination is redirected like real WAN traffic.

## Running

```
make build
make test-netns-unpriv   # unshare -Urnm -- python3 test/netns/run.py ...
make test-netns          # sudo python3 test/netns/run.py ...  (what CI uses)
```

Options: `--binary PATH` (default `dist/tcp-split`), `--keep` (skip cleanup and
print how to inspect). Scratch files live in `tmp/netns/` and are removed
afterwards unless `--keep`. Any FAIL prints the daemon log and exits 1.

Unprivileged mode works on this host (verified); `sudo` is only the CI path.
Under `unshare -Urnm` the harness is root of a new user, network and mount
namespace, so one code path serves both.

Facts about the unprivileged path:

- `/run/netns` does not exist and cannot be created under the root-owned `/run`.
  When `/proc/self/uid_map` is not the identity map (`0 0 4294967295`), the
  harness mounts a tmpfs over `/run` and then creates `/run/netns`. As real
  root it uses `/run/netns` as is.
- `nft` and `sysctl` live in `/usr/sbin` and `/sbin`, which are not on a normal
  user PATH, so the harness appends `/usr/sbin:/sbin` to `PATH`.

## Checks

| Check | Proves |
| --- | --- |
| `echo_v4`, `echo_v6` | 1 MiB each way through the proxy intact; log shows `dst=10.99.1.2:8080` / `dst=[fd99:1::2]:8080` and `result=ok` (interception recovers the original destination) |
| `direct_connect_reset` | a direct connection to the proxy port is not a redirect: client sees `ECONNRESET`, log `result=not-redirected` |
| `denied` | `--deny 10.99.2.0/24` destinations are reset without dialling, log `result=denied` |
| `local_not_split` | traffic to the server itself (`10.99.0.1:22`) is excluded by `fib daddr type != local`: `ECONNREFUSED` and no proxy log line |
| `not_wan_not_split` | a destination routed out of a non-WAN interface (`10.99.3.9:8080` in `lan`) is forwarded, never split: the 1 MiB echo works, the `redirect` counter does not move (`fib daddr oif $WAN_IF` does not match) and the proxy logs nothing (anti-goal 1) |
| `input_guard` | a non-tunnel host cannot reach the proxy port: from `internet`, a connect to the listener `10.99.0.1:10443` times out (2 s) rather than being accepted or refused, the `input` drop counter rises by exactly the SYNs the kernel sent (1 + retransmissions, read from `TCP_INFO`; 2 in a 2 s window), and the proxy logs nothing (anti-goal 2) |
| `nft_counters` | the `redirect` rule counted as many packets as connections the harness made through the redirect (echo_v4, echo_v6, denied: 3; only SYNs traverse the nat hook) and the `input` drop counter equals the SYNs `input_guard` saw dropped, so nothing else was dropped |

## Not covered here

This harness runs no systemd: it loads the example table
(`packaging/examples/redirect.nft`) into its server namespace itself and runs
the binary with `--listen`. In production the rules are permanent site
firewall configuration and the units are enabled at boot; what happens when
the proxy is stopped (new redirected connections are reset) is checked on a
real host, as the spec's acceptance item 5.
