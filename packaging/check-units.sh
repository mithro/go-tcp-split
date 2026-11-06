#!/bin/sh
# Checks the shipped systemd units with systemd-analyze and the example
# nftables ruleset with nft -c. Run from anywhere; `make test` runs it.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$here")
NFT=/usr/sbin/nft

# The shipped socket has no ListenStream= (the site's drop-in supplies it), so
# verify a scratch copy of the units with the example drop-in installed, under
# a scratch --root holding a stub /usr/bin/tcp-split (the service names it).
mkdir -p "$root/tmp"
fakeroot=$root/tmp/unitroot
units=$fakeroot/usr/lib/systemd/system
scratch=$root/tmp/redirect-check.nft
trap 'rm -rf "$fakeroot"; rm -f "$scratch"' EXIT
mkdir -p "$units/tcp-split.socket.d"
mkdir -p "$fakeroot/usr/bin"
printf '#!/bin/sh\n' > "$fakeroot/usr/bin/tcp-split"
chmod 0755 "$fakeroot/usr/bin/tcp-split"
# The units' default dependencies (Requires=sysinit.target and the like) must
# resolve inside --root: systemd 255 (Ubuntu 24.04) refuses the start job
# otherwise, so the scratch root gets the host's standard targets.
cp /usr/lib/systemd/system/*.target "$units/"
cp "$here"/systemd/tcp-split.socket "$here"/systemd/tcp-split.service "$units/"
cp "$here/examples/10-listen.conf" "$units/tcp-split.socket.d/10-listen.conf"
systemd-analyze verify --root="$fakeroot" \
	"$units/tcp-split.socket" \
	"$units/tcp-split.service"

# nft -c needs netlink, and `fib daddr oif "<name>"` is resolved against real
# interfaces, so the example is checked with defines for "lo".
{
	echo 'define VPN_IFS = { "lo" }'
	echo 'define WAN_IF = "lo"'
	echo 'define PORT = 10443'
	cat "$here/examples/redirect.nft"
} > "$scratch"

if [ "$(id -u)" -eq 0 ]; then
	"$NFT" -c -f "$scratch"
elif unshare -Urn true; then
	unshare -Urn "$NFT" -c -f "$scratch"
else
	echo 'check-units: need root or unprivileged user namespaces for nft -c' >&2
	exit 1
fi
echo 'check-units: ok'
