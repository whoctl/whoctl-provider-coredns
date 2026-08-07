#!/bin/sh
# End-to-end test for the coredns provider, against a CoreDNS reading the same
# files it does.
#
# Run it through scripts/e2e-run.sh, which starts that CoreDNS and borrows
# whoctl's container harness for the rest.
#
# # What this checks that nothing else can
#
# Every unit test in this repository parses testdata/ and agrees with itself. A
# parser that is wrong about a Corefile goes on agreeing with itself forever:
# the fixture drifts into something no CoreDNS accepts, or a zone is read with
# the wrong origin, and the tests nod along. There is no way out of that from
# inside.
#
# So the assertions below come in pairs. What whoctl reports about a zone and
# what CoreDNS answers about the same zone have to be the same thing — the
# serial, the name servers, the records, the port it is served on. CoreDNS is
# reading the identical files, so any disagreement is this provider being wrong.
#
# The other half is the default path. On a workstation every command needs
# --root or WHOCTL_COREDNS_COREFILE, so /etc/coredns — the path the provider
# looks at when nothing tells it otherwise — is the one line nothing exercises.
# In here it is the only one that runs, and no command below passes either.
set -u

if [ "${WHOCTL_IN_CONTAINER:-}" != "1" ]; then
	echo "run this through scripts/e2e-run.sh, which brings a CoreDNS with it" >&2
	exit 1
fi

dns="${COREDNS_HOST:-dns}"

passed=0
failed=0

ok() { passed=$((passed + 1)); echo "  ok    $1"; }
nok() { failed=$((failed + 1)); echo "  FAIL  $1"; [ $# -gt 1 ] && echo "        $2"; }

# expect_match DESCRIPTION PATTERN COMMAND...
expect_match() {
	desc=$1
	pattern=$2
	shift 2
	out=$("$@" 2>&1)
	if printf '%s' "$out" | grep -qE "$pattern"; then
		ok "$desc"
	else
		nok "$desc" "expected /$pattern/ in: $out"
	fi
}

# expect_no_match DESCRIPTION PATTERN COMMAND...
expect_no_match() {
	desc=$1
	pattern=$2
	shift 2
	out=$("$@" 2>&1)
	if printf '%s' "$out" | grep -qE "$pattern"; then
		nok "$desc" "did not expect /$pattern/ in: $out"
	else
		ok "$desc"
	fi
}

# expect_count DESCRIPTION N COMMAND... — rows of output, header excluded.
expect_count() {
	desc=$1
	want=$2
	shift 2
	got=$("$@" 2>&1 | tail -n +2 | grep -c .)
	if [ "$got" = "$want" ]; then
		ok "$desc"
	else
		nok "$desc" "got $got rows, want $want"
	fi
}

# expect_eq DESCRIPTION WANT GOT — the shape every agreement check takes.
expect_eq() {
	if [ "$2" = "$3" ]; then
		ok "$1"
	else
		nok "$1" "whoctl says '$3', CoreDNS says '$2'"
	fi
}

# field PATH ARGS... — one value out of a manifest.
#
# The quotes are stripped because `yq` is two different programs wearing one
# name across distros and only one of them writes raw. This suite runs on
# Alpine, but a value that changes shape with the distro is a trap left for
# whoever runs it somewhere else.
field() {
	path=$1
	shift
	whoctl get "$@" -o yaml 2>/dev/null | yq "$path" 2>/dev/null | tr -d '"'
}

section() { echo; echo "== $1"; }

echo "whoctl e2e — coredns, against a CoreDNS at $dns reading the same files"

section "the default path"
# Not one command in this file passes --root or names a Corefile. That is the
# assertion: the fixture is at /etc/coredns and the provider found it alone.
expect_match "the Corefile is found with nothing pointing at it" '^NAME' whoctl get coredns/servers
expect_match "and it is the one at /etc/coredns" '/etc/coredns/Corefile' \
	sh -c "whoctl get coredns/servers -o wide"

section "discovery and addressing"
expect_match "the kinds are addressed under one flat group" '^coredns/servers' whoctl resources
expect_match "which is coredns.whoctl.io" 'coredns\.whoctl\.io/v1alpha1' whoctl resources
expect_match "kubectl's dotted form reaches the same kind" '^NAME' whoctl get coredns/zones.coredns
expect_match "and so does the short name" '^NAME' whoctl get coredns/srv

section "server blocks"
expect_count "every block in the Corefile is listed" 3 whoctl get coredns/servers
expect_no_match "and the snippet is not one of them" 'logging' whoctl get coredns/servers
expect_match "the root block is named for the zone it is" '^root-53' whoctl get coredns/servers
expect_match "a block is found by a zone it answers for" 'example.com-53' \
	whoctl get coredns/server example.com
expect_match "the trailing dot is optional" 'example.com-53' whoctl get coredns/server example.com.
expect_match "a zone nobody serves is not found" 'not found' whoctl get coredns/server nowhere.invalid

# The snippet's two plugins have to be in the block that imports it, or the
# object describes a server that is not the one running.
expect_eq "an imported snippet's plugins are counted in the block" 5 \
	"$(field .status.pluginCount coredns/server example.com-53)"
expect_match "and are named in the chain" 'name: log' \
	sh -c "whoctl get coredns/server example.com-53 -o yaml"

section "zones"
expect_count "every zone the Corefile loads is listed" 3 whoctl get coredns/zones
expect_match "a zone is named by its origin" '^example\.com ' whoctl get coredns/zones
expect_eq "a file with no \$ORIGIN takes the one the directive names" "internal.test." \
	"$(field .spec.origin coredns/zone internal.test)"
expect_eq "and the block's root directive found its file" \
	"/etc/coredns/zones/db.internal.test" "$(field .status.path coredns/zone internal.test)"

section "whoctl and CoreDNS agree"
# The pair that matters. CoreDNS is reading the identical files, so anything
# below that disagrees is this provider being wrong about them.
for zone in example.com example.org; do
	soa=$(dig +short "@$dns" "$zone" SOA)
	expect_eq "$zone: CoreDNS is authoritative for it" 1 "$(printf '%s' "$soa" | grep -c .)"
	expect_eq "$zone: the serial is the same" \
		"$(printf '%s' "$soa" | awk '{print $3}')" "$(field .status.serial coredns/zone "$zone")"
	expect_eq "$zone: the primary name server is the same" \
		"$(printf '%s' "$soa" | awk '{print $1}')" \
		"$(field .status.primaryNameServer coredns/zone "$zone")"
	expect_eq "$zone: the responsible mailbox is the same" \
		"$(printf '%s' "$soa" | awk '{print $2}')" "$(field .status.mailbox coredns/zone "$zone")"
done

expect_eq "the apex name servers are the same, and in the same order" \
	"$(dig +short "@$dns" example.com NS | sort | tr '\n' ' ')" \
	"$(field '.status.nameServers | join(" ")' coredns/zone example.com | sed 's/ $//' | tr ' ' '\n' | sort | tr '\n' ' ')"

# A record whoctl counted has to be a record CoreDNS answers with. Three types,
# because the parser reads each of them down a different branch: a CNAME whose
# owner is relative, an A with its own TTL before the class, and a TXT whose
# rdata holds the semicolon that ends a comment everywhere else.
expect_eq "the relative CNAME resolves to what the file says" "example.com." \
	"$(dig +short "@$dns" www.example.com CNAME)"
expect_eq "the A with a TTL before its class is served" "192.0.2.80" \
	"$(dig +short "@$dns" api.example.com A)"
expect_match "the TXT keeps the semicolon inside its quotes" 'rua=mailto:dmarc@example\.com' \
	dig +short "@$dns" _dmarc.example.com TXT

section "the port a block listens on"
# internal.test is the only block not on 53, and getting the port wrong is
# invisible from inside: the object would still list a zone that is served,
# just not where it says.
expect_eq "whoctl reads it off the label" "5353" "$(field .spec.port coredns/server internal.test-5353)"
expect_eq "and CoreDNS is authoritative there" 1 \
	"$(dig +short -p 5353 "@$dns" internal.test SOA | grep -c .)"
expect_eq "with the serial whoctl reported" \
	"$(dig +short -p 5353 "@$dns" internal.test SOA | awk '{print $3}')" \
	"$(field .status.serial coredns/zone internal.test)"

section "forwarding"
expect_match "the upstreams of the catch-all block are read" '1\.1\.1\.1' \
	whoctl get coredns/servers
expect_match "both of them" '9\.9\.9\.9' \
	sh -c "whoctl get coredns/server root-53 -o yaml"
expect_match "and the plugin's own block is flagged, not modelled" 'block: true' \
	sh -c "whoctl get coredns/server root-53 -o yaml"

section "what is not followed is said out loud"
# A different fixture: a Corefile whose imports go nowhere and whose zone file
# is not there. A listing that is quietly short reads exactly like a machine
# that is quietly short, which is the failure this is about.
broken=/testdata/imports/etc/coredns/Corefile
expect_match "a file import is reported rather than passed over" 'conf\.d' \
	sh -c "WHOCTL_COREDNS_COREFILE=$broken whoctl get coredns/servers -o yaml"
expect_match "an unknown snippet too" 'nowhere' \
	sh -c "WHOCTL_COREDNS_COREFILE=$broken whoctl get coredns/servers -o yaml"
expect_count "a zone whose file is missing is still listed" 1 \
	sh -c "WHOCTL_COREDNS_COREFILE=$broken whoctl get coredns/zones"
expect_match "and says the file is not there" 'not there' \
	sh -c "WHOCTL_COREDNS_COREFILE=$broken whoctl get coredns/zones -o wide"

section "a Corefile that is not there"
expect_match "names the path it looked at" '/nowhere/Corefile' \
	sh -c "WHOCTL_COREDNS_COREFILE=/nowhere/Corefile whoctl get coredns/servers"

section "this provider does not write yet"
expect_match "apply is refused, and says why" 'does not rewrite' sh -c "whoctl apply -f - <<'YAML'
apiVersion: coredns.whoctl.io/v1alpha1
kind: Server
metadata:
  name: example.com-53
spec:
  zones: [example.com.]
  port: \"53\"
YAML"
expect_match "delete is refused too" 'does not rewrite' whoctl delete coredns/server root-53
expect_match "and a zone the same way" 'does not rewrite' whoctl delete coredns/zone example.com

# What stops a *Kubernetes* client offering an edit is ResourceType.Verbs
# holding get and list and nothing else. That has no surface on the command
# line — `whoctl resources` prints capabilities, which is a different
# vocabulary for a different audience — so it is pinned where it is visible,
# in each kind's TestItSaysItIsReadOnly.

echo
echo "passed: $passed  failed: $failed"
[ "$failed" -eq 0 ]
