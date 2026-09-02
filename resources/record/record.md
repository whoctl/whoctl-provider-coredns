---
verbs: [get, apply, delete, describe]
---

# Record

A resource record in a zone file — and the one thing this provider writes.

## Example

```console
$ whoctl get coredns/records
NAME                     ZONE           TYPE    VALUES              TTL    MANAGED
ns1-ns1.example.com      example.com.   A       192.0.2.53          0      false
www-www.example.com      example.com.   CNAME   example.com.        0      false
a-host1.example.com      example.com.   A       192.0.2.10          0      true

$ whoctl apply -f record.yaml
$ whoctl delete coredns/record a-host1.example.com
$ whoctl get coredns/records --field-selector status.managed=true
```

```yaml
apiVersion: coredns.whoctl.io/v1alpha1
kind: Record
spec:
  zone: example.com.
  name: host1
  type: A
  values: ["192.0.2.10"]
```

## What whoctl owns, and what it will not touch

A zone file is somebody's, and most of it was written by hand. whoctl owns
exactly what is between its markers:

```
; whoctl:begin whoctl
host1.example.com.	IN	A	192.0.2.10
; whoctl:end whoctl
```

Records inside the region are created, updated and deleted. Records outside it
are **listed** — a listing that showed only whoctl's own records would be one
nobody could trust — and every attempt to change one is refused, naming the file
and the line.

That is what makes writing to a file a DNS server is reading acceptable: the
ownership is visible in the file, to a person, rather than held in something
whoctl remembers. `status.managed` says which side of the line a record is on.

Everything outside the markers survives byte for byte: the hand-aligned SOA, the
comment after a field, the blank line somebody left between two groups, and the
records this provider's parser does not understand.

## A second writer names its own region

`spec.region` picks which marked region a record belongs to, defaulting to
`whoctl`. Two writers keeping records in one zone each name their own:

```
; whoctl:begin leases-to-dns
; whoctl:end leases-to-dns
```

Pruning one region never reaches the other's records, which is what makes it
safe for something automatic — a lease-to-DNS adapter, say — to delete what it
no longer wants without knowing what else is in the file.

## The serial is bumped on every change

A secondary decides whether to transfer by comparing serials, and CoreDNS
decides whether a reload changed anything the same way. So every write
increments the SOA serial by one, in place: `2026080501` becomes `2026080502`,
the alignment and the `; serial` comment untouched.

Plus one, and nothing cleverer. The only requirement is that it goes up;
rewriting it as today's date would impose a convention on a file that may not be
using one, and runs out after a hundred changes in a day.

A zone with no SOA is refused rather than written to. Without one CoreDNS does
not answer for the zone at all, and writing records into it would be reporting
success for something nobody will ever be served.

## One name and type is one object

`www IN A 192.0.2.1` and `www IN A 192.0.2.2` are a round robin: **one** Record
with two `values`, not two records. Splitting them would make an apply that sets
both an apply that has to delete one first.

`metadata.name` is the type and the fully qualified name — `a-host1.example.com`
— because that pair is what identifies a record. It is spelled with a dash
rather than a slash because a Kubernetes name may hold only letters, digits,
dashes and dots, and these objects are served to kubectl and k9s. `get` also
takes the fully qualified name on its own, and `www.example.com A`.

## Names are written absolute

A record whoctl writes carries its fully qualified name, ending in a dot, even
when the file uses relative names everywhere else. A relative name means
whatever the last `$ORIGIN` above it said — and whoctl's region can be anywhere
in the file, including below one somebody adds later. An absolute name is the
same record wherever the region ends up.

## What it does not check

Rdata against its type. `values: ["not an address"]` is written as given. That
is a resolver's job, and being wrong about it here would be worse than not
answering — CoreDNS refuses to load a zone it cannot parse, which is a check by
something that knows.

The SOA is refused as a type: its serial is maintained by this provider on every
write, so a managed SOA would be whoctl arguing with itself.

## Spec

<!-- whoctl:begin spec -->
| Field | Type | Notes | Description |
| --- | --- | --- | --- |
| `zone` | string | **required** | The zone that holds it, as the Corefile names it. Example: `example.com.`. |
| `name` | string | **required** | The owner name, relative to the zone or absolute. @ is the zone itself. Example: `host1`. |
| `type` | string | **required** | The record type: A, AAAA, CNAME, TXT, MX, and whatever else the zone holds. Example: `A`. |
| `values` | list of string | **required** | The rdata, one entry per record. Two values of one name and type is a round robin: one object here, two lines in the file. Example: `192.0.2.10`. |
| `ttl` | integer | optional | Seconds. Omitted takes the zone's own default, which is what a hand-written record usually does. Example: `3600`. |
| `region` | string | optional | Which marked region of the file this belongs to. Empty is whoctl's own; a second writer names its own, so that pruning one never reaches the other's. Example: `leases-to-dns`. |
<!-- whoctl:end spec -->

## Status

<!-- whoctl:begin status -->
| Field | Type | Description |
| --- | --- | --- |
| `fqdn` | string | The owner name, absolute, which is what the file actually holds. Example: `host1.example.com.`. |
| `file` | string | The zone file it is in. Example: `/etc/coredns/db.example.com`. |
| `managed` | boolean | Whether it sits inside a whoctl region. A record that does not is read here and refused every change. Example: `true`. |
| `region` | string | The region it was found in, when it is in one. |
<!-- whoctl:end status -->
