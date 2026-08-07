#!/bin/sh
# Starts a CoreDNS serving this repository's fixture, and leaves it running.
#
# The sandbox starts and stops one of these around itself. This is the same
# CoreDNS without the sandbox, for when what you want is to type `dig` at it and
# look at what comes back.
#
# # Why a real CoreDNS at all, when the provider only reads files
#
# Because the provider only reads files. Every test in this repository parses
# testdata/ and agrees with itself, and would go on agreeing if the parser were
# wrong — the fixture would drift into something no CoreDNS accepts and nothing
# would say so. A CoreDNS answering from the same files is the one check that
# comes from outside: what `whoctl get coredns/zones` reports and what `dig`
# answers have to be the same zones.
#
# Usage:
#   scripts/coredns.sh                 # start it on a free port and say how to reach it
#   COREDNS_PORT=5300 …/coredns.sh     # pin the port, so a script can hardcode it
#   scripts/coredns.sh stop            # take it down
#
# The port floats by default because a fixed one collides with whatever else on
# the machine already wants it — and 53 is held by a resolver on most of them.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
engine="${CONTAINER_ENGINE:-podman}"
image="${COREDNS_IMAGE:-docker.io/coredns/coredns:1.12.0}"
name=whoctl-coredns

if [ "${1:-}" = stop ]; then
	"$engine" rm -f "$name" >/dev/null 2>&1 || true
	echo "stopped"
	exit 0
fi

"$engine" rm -f "$name" >/dev/null 2>&1 || true

publish="127.0.0.1::53/udp"
if [ -n "${COREDNS_PORT:-}" ]; then
	publish="127.0.0.1:$COREDNS_PORT:53/udp"
fi

# Read-only: this is the same tree the provider reads, and a CoreDNS that could
# rewrite the fixture would make the two disagree in the one direction nothing
# would catch.
if ! "$engine" run -d --rm --name "$name" \
	-p "$publish" \
	-v "$root/testdata/etc/coredns:/etc/coredns:ro,z" \
	-w /etc/coredns \
	"$image" -conf /etc/coredns/Corefile >/dev/null; then
	echo "could not start CoreDNS on $publish; something else may hold that port" >&2
	exit 1
fi

port=$("$engine" port "$name" 53/udp | head -1 | sed 's/.*://')

# It answers before it prints anything useful, so the readiness check is a query
# rather than a log line.
ready=0
for _ in $(seq 1 30); do
	if "$engine" exec "$name" /coredns -version >/dev/null 2>&1 || [ -n "$port" ]; then
		ready=1
		break
	fi
	sleep 1
done
if [ "$ready" -ne 1 ]; then
	echo "CoreDNS never came up:" >&2
	"$engine" logs "$name" >&2 || true
	exit 1
fi

cat <<EOF

CoreDNS is up, serving $root/testdata/etc/coredns.

  dig @127.0.0.1 -p $port example.com SOA +short
  whoctl get coredns/zones --root $root/testdata

The two are reading the same files, so they have to agree. Take it down with:

  make coredns-stop
EOF
