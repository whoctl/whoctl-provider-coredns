#!/bin/sh
# Opens a shell on a throwaway machine with this provider ready to use.
#
# The container harness is whoctl's — running whoctl and some providers on a
# throwaway machine is its job, and it does not care which distro it is. What
# stays here is what is this provider's: the Corefile tree, and a CoreDNS
# answering from it.
#
# # What is prepared, and why each piece
#
# The fixture is mounted at /etc/coredns, which is the path the provider looks
# at when nothing tells it otherwise. That is deliberate: on a workstation every
# command needs --root or an environment variable, so the default path is the
# one line of this provider that nothing ever exercises. In here it is the only
# one that runs.
#
# A CoreDNS is started beside the container, reading the same files, so the two
# readings can be compared. `whoctl get coredns/zones` and `dig` have to agree,
# and a parser that is wrong about a Corefile agrees with itself right up until
# something outside it is asked.
#
# The whole of testdata/ is mounted a second time at /testdata, because the
# suite needs the fixtures that are deliberately *not* the default one — the
# Corefile whose imports go nowhere, and the zone file that is not there.
#
# Usage:
#   scripts/sandbox.sh                              # a shell
#   scripts/sandbox.sh whoctl get coredns/servers   # one command
#
# Alpine, and only Alpine. The distro matters to the linux provider, which
# drives four package managers and two sets of account tools; nothing here
# touches a system at all — it reads files — so a second distro would cost
# minutes per run and answer no question.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
sandbox="${WHOCTL_SANDBOX:-$root/../whoctl/scripts/sandbox.sh}"
engine="${CONTAINER_ENGINE:-podman}"
image="${COREDNS_IMAGE:-docker.io/coredns/coredns:1.12.0}"
network=whoctl-coredns-sandbox
dns=whoctl-coredns-sandbox-dns

if [ ! -x "$sandbox" ]; then
	echo "no sandbox to run in: check out github.com/whoctl/whoctl beside this" >&2
	echo "repository, or set WHOCTL_SANDBOX to its scripts/sandbox.sh." >&2
	exit 1
fi

cleanup() {
	"$engine" rm -f "$dns" >/dev/null 2>&1 || true
	"$engine" network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
cleanup

"$engine" network create "$network" >/dev/null 2>&1 || true

# Read-only, and on the shared network rather than published: nothing out here
# needs to reach it, and not publishing is one fewer port to collide with the
# resolver already on this machine.
# The alias is what makes `dig @dns` work: the container's own name is long and
# nobody would type it, and a name somebody will not type is a comparison
# nobody will make.
"$engine" run -d --rm --name "$dns" --network "$network" --network-alias dns \
	-v "$root/testdata/etc/coredns:/etc/coredns:ro,z" \
	-w /etc/coredns \
	"$image" -conf /etc/coredns/Corefile >/dev/null

cat >&2 <<EOF
coredns sandbox — the fixture is at /etc/coredns, and a CoreDNS is answering
from the same files at "dns". Inside:

  whoctl get coredns/servers
  whoctl get coredns/zones
  dig @dns example.com SOA +short

EOF

PROVIDERS=coredns \
EXTRA_PACKAGES="bind-tools" \
MOUNTS="-v $root/testdata/etc/coredns:/etc/coredns:ro,z \
	-v $root/testdata:/testdata:ro,z \
	-v $root/scripts:/scripts:ro,z" \
NETWORK="--network $network" \
ENV="-e COREDNS_HOST=$dns" \
	"$sandbox" "$@"
