---
title: CoreDNS
---

# CoreDNS

Reads a local CoreDNS: the server blocks of its Corefile, and the zone files
those blocks load.

```console
$ whoctl get coredns/servers
NAME                ZONES                       PORT   PLUGINS   UPSTREAM   AGE
example.com-53      example.com.,example.org.   53     6         -          6d
internal.test-5353  internal.test.              5353   3         -          6d
root-53             .                           53     7         1.1.1.1    6d

$ whoctl get coredns/zones
NAME            RECORDS   SERIAL       TTL    SERVERS              AGE
example.com     10        2026080501   3600   example.com-53       6d
example.org     3         2026080502   300    example.com-53       6d
internal.test   5         2026080503   3600   internal.test-5353   6d
```

## What it reads, and where from

The Corefile, and the zone files it points at. In order:

1. `--root`'s `etc/coredns/Corefile`, which for an empty root is
   `/etc/coredns/Corefile` — where every distribution package and the upstream
   systemd unit put it.
2. `WHOCTL_COREDNS_COREFILE`, when that names something else.

CoreDNS's own default is `Corefile` in the working directory, which is right
for running it by hand and useless for describing a machine: which directory
that was depends on how the daemon happened to be started.

The environment variable is how a whoctl server points a context at one CoreDNS
among several. The provider cannot tell whether a person or a server started
it, which is deliberate — it behaves as whoever did.

## It reads and does not write

Both kinds publish `get` and `list` and nothing else, so a Kubernetes client
greys the edit out rather than offering one that fails.

The reason is not that a Corefile cannot be written. It is that rewriting one
means giving back every plugin, argument and comment that was not modelled, and
CoreDNS has more plugins than this provider ever will. The parser already keeps
the raw source of everything it did not interpret, which is the half of that
which can be proven now; the other half is the rewrite, and it is not written.

## What is deliberately not followed

A listing that is quietly short reads exactly like a machine that is quietly
short, so these are reported in `status.warnings` and `status.message` rather
than passed over:

| In the file | What happens |
| --- | --- |
| `import` of a snippet defined in the same Corefile | Expanded. The block really does have those plugins. |
| `import` of another *file* | Not followed. It globs and recurses, and the result would match no file on disk. |
| `$INCLUDE` in a zone file | Not followed, and the records it holds are not counted. |
| `$GENERATE` | Not expanded, and the records it makes are not counted. |
| `auto`, `kubernetes`, `etcd`, `route53` | Not zone files. No Zone object is invented for them. |
| `{$VAR}` | Substituted from the environment, because CoreDNS substitutes it. |

## Kinds

| Kind | What it is |
| --- | --- |
| [Server](server/server.md) | One server block: its zones, its port, its plugin chain. |
| [Zone](zone/zone.md) | One zone file: its SOA, and what it holds. |
