---
verbs: [get, describe]
---

# Zone

A zone file CoreDNS loads: its SOA, how many records it holds and of what
types. Read-only — this provider reads a zone file and does not rewrite one.

## Example

```console
$ whoctl get coredns/zones
NAME            RECORDS   SERIAL       TTL    SERVERS              AGE
example.com     10        2026080501   3600   example.com-53       6d
example.org     3         2026080502   300    example.com-53       6d
internal.test   5         2026080503   3600   internal.test-5353   6d

$ whoctl get coredns/zone example.com -o yaml
```

## It is discovered, not declared

A zone is not something the Corefile contains — it is a file the Corefile points
at, through a `file` directive. So listing zones means reading the Corefile to
find out which files are loaded for which origins, and then reading those.

Only zones reached through `file` are here. The `auto` plugin loads a directory
by pattern, and `kubernetes`, `etcd` and `route53` answer from somewhere that is
not a file at all. None of those is a zone file, and inventing an object for one
would be describing something this provider cannot read.

## The name is the origin

`example.com`, without the trailing dot, because a Kubernetes name may not end
in one. The origin comes from the file's `$ORIGIN` when it has one and from the
`file` directive otherwise, which is the order CoreDNS resolves it in.

The same file loaded by two server blocks is one zone with two entries in
`status.servers` — a Corefile listening on 53 and on 5353 writes the directive
twice, and that is one zone. Two *different* files for one origin stays two
objects, because it is two things.

## A zone that cannot be read is still an object

If the Corefile loads a file that is not there, the Zone is listed with zero
records and `status.message` saying so. Leaving it out would read as "not
configured", when what is true is "configured and broken" — the more urgent of
the two, and the one worth being able to see.

The same applies to a file with no SOA, which is not a zone CoreDNS will answer
for.

## What the parser does and does not do

It reads the master file format RFC 1035 §5 defines, including the three details
every real zone file relies on: a record with no owner field inherits the
previous one's, parentheses continue a record across lines, and the TTL and the
class are both optional and may appear in either order. TTLs are read in seconds
and in BIND's spelling, so `1h`, `15m` and `2w` all work.

It does not validate rdata against its type, follow `$INCLUDE`, or expand
`$GENERATE`. The last two would make the record count wrong without saying so,
which is why they land in `status.message` instead.

## It is global

See [Server](../server/server.md): one CoreDNS, no second axis, so no namespace.

## Spec

<!-- whoctl:begin spec -->
| Field | Type | Notes | Description |
| --- | --- | --- | --- |
| `origin` | string | **required** | The zone's own name, absolute. Example: `example.com.`. |
| `file` | string | **required** | The zone file, exactly as the Corefile's file directive names it. Example: `db.example.com`. |
| `ttl` | integer | optional | The zone's default TTL in seconds, from $TTL. Zero when the file states none. Example: `3600`. |
<!-- whoctl:end spec -->

## Status

<!-- whoctl:begin status -->
| Field | Type | Description |
| --- | --- | --- |
| `path` | string | The path actually opened, once the Corefile's own was resolved. Example: `/etc/coredns/db.example.com`. |
| `servers` | list of string | The server blocks that load this zone. More than one is ordinary: the same file often answers on two ports. |
| `serial` | integer | The SOA serial, which is what a secondary compares to decide whether to transfer. Example: `2026080501`. |
| `refresh` | integer | SOA refresh, in seconds. |
| `retry` | integer | SOA retry, in seconds. |
| `expire` | integer | SOA expire, in seconds. |
| `minimum` | integer | SOA minimum, which is the negative-answer TTL. |
| `primaryNameServer` | string | The SOA's primary name server. Example: `ns1.example.com.`. |
| `mailbox` | string | The SOA's responsible mailbox, in DNS spelling — the first dot stands for the @. Example: `hostmaster.example.com.`. |
| `records` | integer | How many resource records the file holds. |
| `recordTypes` | map of integer | How many of each type, so a zone can be sized up without listing it. |
| `nameServers` | list of string | The NS records at the apex: what the parent delegates to. |
| `message` | string | Why the numbers above are missing or short — the file could not be read, or it uses something that was not expanded. |
<!-- whoctl:end status -->
