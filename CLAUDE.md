# whoctl-provider-coredns

Reads a local CoreDNS: the server blocks of its Corefile, and the zone files
those blocks load. Two kinds, `Server` and `Zone`, both read-only.

The safety rules are in the workspace `CLAUDE.md`. They bind here too, but this
provider is the mildest of the four: it opens files and never writes one. What
that buys is that `go test ./...` is safe on the host — every test reads
`testdata/` and nothing else.

## Decisions somebody would otherwise undo

**The parser keeps the source of everything it did not model.** CoreDNS has more
plugins than this provider ever will, so `internal/corefile` parses the *shape*
— blocks, directive names, arguments, nesting — and holds the raw lines of the
rest. That is not tidiness. The day this writes a Corefile, the rewrite has to
give back every plugin, argument and comment it did not understand, and it can
only do that if it still has them. A model that throws the rest away is how a
DNS server comes back up missing half its configuration.

This is the same bargain the linux provider struck with `resolv.conf`, and it is
why both kinds publish `get, list` and nothing else: the keeping is proven, the
giving-back is not written.

**The group is flat.** `coredns.whoctl.io`, so the address is `coredns/servers`
— two segments. The aws provider needs a third because Instance exists under
`ec2` and under `rds` and one must not answer for the other. CoreDNS is one
thing and nothing here collides.

**A Server's name is its first zone and its port.** `example.com-53`,
`root-53`. Not a guess at uniqueness: CoreDNS refuses to start when two blocks
claim the same zone on the same port, so no other block can hold this one's
first zone at this port. The dash is because a colon is not legal in a
Kubernetes object name, and these objects are served to kubectl and k9s —
`corefile.SanitizeName` is where that is enforced and it applies to zone names
too, for the `_tcp.example.com` case.

**A Zone is discovered, not declared.** It is a file the Corefile points at
through `file`, so listing zones means reading the Corefile first. Only `file`
counts: `auto` loads a directory by pattern, and `kubernetes`, `etcd` and
`route53` answer from somewhere that is not a file at all. Inventing a Zone for
those would be describing something this provider cannot read.

**A zone whose file is missing is still an object.** Leaving it out reads as
"not configured" when the truth is "configured and broken", which is the more
urgent of the two. `status.message` says which.

**What is not followed is said out loud.** A file `import`, `$INCLUDE`,
`$GENERATE` — each lands in `status.warnings` or `status.message` rather than
being passed over, because a listing that is quietly short is indistinguishable
from a machine that is quietly short. A snippet `import` *is* expanded, because
the block really does have those plugins and reporting it without them would be
reporting something that is not running.

**`{$VAR}` is substituted, because CoreDNS substitutes it.** A block listening
on `{$PORT}` is listening on a number, and reporting the literal would be
reporting something untrue. The cost is that what this reads depends on the
environment — which is the same thing the aws credential chain does, and for the
same reason.

**`WHOCTL_COREDNS_ROOT` exists because whoctl's `--root` is one value for the
session.** A server serving three CoreDNS trees needs three, and a context can
only set an environment. Without it a context could name a Corefile anywhere and
still have `root /etc/coredns/zones` — which is ordinary — resolve against the
server's own filesystem. The unit tests did not catch that; the server did, on
the first request. `internal/provider/provider_test.go` pins it.

**`metadata.creationTimestamp` is the file's modification time.** A Corefile
records no creation time. A blank AGE column says nothing; "this configuration
last changed 6d ago" is the useful answer, and it is stated in the doc tag
rather than left to be discovered.

## The two parsers

`internal/corefile` is Caddyfile syntax. Three things about it are not obvious
and all three come from Caddy's own lexer, which is what CoreDNS runs: a `#`
outside quotes starts a comment wherever it appears, a directive ends at the end
of its line, and a block's `{` is on the head's line. The last one is accepted
leniently — a lone `{` is never a plugin name, so taking it on the next line
turns a misparse into a parse.

`internal/zonefile` is RFC 1035 §5. Three details every real zone file relies on
and a parser that misses any one of them is silently wrong: a record with no
owner field inherits the previous one's, parentheses continue a record across
lines, and the TTL and the class are both optional in either order. TTLs are read
in seconds and in BIND's `1h`/`15m`/`2w` spelling.

Neither validates rdata against its type. Neither ever will: that is a resolver's
job, and being wrong about it would be worse than not answering.

## `make validate` is the only check from outside

Every test here reads `testdata/`, and every one of them would keep passing if
the fixture drifted into something CoreDNS refuses to load — the parser would be
exercised against a file no CoreDNS has ever accepted, and the tests would agree
with it.

`scripts/validate.sh` runs the real CoreDNS in a container against the fixture
and checks it comes up. It has no `--validate` flag, so the check is that it
serves for five seconds rather than exiting; `--network=none` is what keeps it
off the machine's own port 53. Without podman or docker it says so and passes,
because a developer without a container runtime is not a broken checkout.

**Change the fixture and run it.** The fixture is the thing that has to stay
true, not the parser.

## Layout

`internal/corefile` and `internal/zonefile` are the two parsers and import
nothing of this provider. `internal/provider` is the shared state — where the
Corefile is, what paths resolve under. `internal/coredns` is assembly and the
overview page. `resources/server` and `resources/zone` are the kinds, each with
its page beside it.
