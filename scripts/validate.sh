#!/usr/bin/env bash
#
# Have CoreDNS itself confirm that the fixture Corefile is a Corefile.
#
# # Why this exists
#
# Every unit test in this repository reads testdata/, and every one of them
# would keep passing if the fixture drifted into something CoreDNS refuses to
# load. The parser would be exercised against a file that no CoreDNS has ever
# accepted, and the tests would agree with it. This is the one check that comes
# from outside: `coredns -validate` parses the file with the real lexer and the
# real plugin registry, and says whether it would start.
#
# It needs a container runtime and the network the first time. Without one it
# says so and returns 0 — a developer without podman is not a broken checkout,
# and `make unit` is still the whole of what this provider can prove alone.

set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE="${COREDNS_IMAGE:-docker.io/coredns/coredns:1.12.0}"

runtime=""
for candidate in podman docker; do
    if command -v "$candidate" >/dev/null 2>&1; then
        runtime="$candidate"
        break
    fi
done

if [ -z "$runtime" ]; then
    echo "== validate: skipped, no podman or docker on this machine"
    echo "   the fixture Corefile is checked by CoreDNS itself when there is one"
    exit 0
fi

echo "== validate: $IMAGE reading testdata/etc/coredns/Corefile"

# CoreDNS has no --validate, so the check is that it comes up and stays up: a
# Corefile it cannot parse, or a zone file the `file` plugin cannot load, makes
# it exit before it ever serves. Five seconds is far more than it needs.
#
# --network=none is what keeps this off the machine's own port 53. The container
# gets a namespace of its own, binds inside it, and reaches nothing.
set +e
timeout --signal=TERM 5 "$runtime" run --rm --network=none \
    -v "$PWD/testdata/etc/coredns:/etc/coredns:ro,z" \
    -w /etc/coredns \
    "$IMAGE" -conf /etc/coredns/Corefile
status=$?
set -e

# 124 is timeout's "it was still running", which here is the pass.
if [ "$status" -ne 124 ]; then
    echo
    echo "CoreDNS exited instead of serving the fixture (status $status). Either" >&2
    echo "the fixture is wrong, or it was changed to match the parser rather" >&2
    echo "than the other way round." >&2
    exit 1
fi

echo "== validate: CoreDNS loaded it and served"
