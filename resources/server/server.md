---
verbs: [get, describe]
---

# Server

One server block of a Corefile: the zones it answers for, the port it answers
on, and the plugin chain that does the answering. Read-only — this provider
reads a Corefile and does not rewrite one.

## Example

```console
$ whoctl get coredns/servers
NAME                ZONES                       PORT   PLUGINS   UPSTREAM   AGE
example.com-53      example.com.,example.org.   53     6         -          6d
internal.test-5353  internal.test.              5353   3         -          6d
root-53             .                           53     7         1.1.1.1    6d

$ whoctl get coredns/server example.com -o yaml
```

## The name is the first zone and the port

A block can be authoritative for several zones at once — `example.com
example.org:53 { … }` is one server — so no single zone is *the* name in
general. The first one is enough to be unique, and not by luck: CoreDNS refuses
to start when two blocks claim the same zone on the same port, so no other block
can hold this one's first zone at this port.

The port is part of the name because serving a zone on 53 and on 1053 is two
blocks and two objects. It is spelled with a dash rather than a colon because a
colon is not legal in a Kubernetes object name, and these objects are served to
kubectl and k9s.

The root zone is `.`, and a name may not begin with a dot, so it is spelled
`root`: `.:53` is `root-53`.

`get` also takes a zone, because the `-53` is what nobody types:

```sh
whoctl get coredns/server example.com        # by a zone it answers for
whoctl get coredns/server example.com.       # the trailing dot is optional
whoctl get coredns/server example.com-53     # by name
whoctl get coredns/server example.com:5353   # by the label as written
```

When a zone is answered for by more than one block, the answer is an error
listing them rather than whichever came first.

## The plugin chain is what the file says, not what runs

`spec.plugins` is in the order the Corefile writes them. That is *not* the order
CoreDNS executes them in — plugin order is fixed when CoreDNS is built, in
`plugin.cfg`, and rearranging the file changes nothing. So this is a faithful
reading of the configuration and not a description of the request path.

Each plugin is a name, its arguments, and whether it opened a block. What is
inside that block is not modelled: it is where the forty-odd plugins differ from
each other most, and the parser keeps its raw source instead.

## AGE is when the Corefile last changed

A Corefile records no creation time, so `metadata.creationTimestamp` is the
file's modification time. A blank AGE column would say nothing; "this
configuration last changed 6d ago" is the useful answer, and it is the same
answer for every block in the file.

## It is global

There is one CoreDNS behind this provider and no second axis to divide it
along, so this kind is cluster-scoped: it has no namespace and `-n` does nothing
to it, the same way a Node ignores it in Kubernetes.

## Spec

<!-- whoctl:begin spec -->
| Field | Type | Notes | Description |
| --- | --- | --- | --- |
| `zones` | list of string | **required** | The zones the block is authoritative for, normalized the way CoreDNS normalizes them: lowercase and absolute. Example: `example.com.`. |
| `port` | string | **required** | The port it listens on, defaulted from the scheme when the label omits it. Example: `53`. |
| `addresses` | list of string | **required** | The block's labels exactly as written, schemes and CIDR notation included. Example: `dns://example.com:53`. |
| `plugins` | list of object | optional | The plugin chain, in the order it is written. The order in the file is not the order CoreDNS runs them in — that is fixed at build time — so this is what the file says, not what happens. |
<!-- whoctl:end spec -->

## Status

<!-- whoctl:begin status -->
| Field | Type | Description |
| --- | --- | --- |
| `corefile` | string | The file it was read from. Example: `/etc/coredns/Corefile`. |
| `line` | integer | The line the block opens on, so an editor can be pointed at it. |
| `pluginCount` | integer | How many plugins the chain has. |
| `zoneFiles` | list of string | The zone files this block loads, as its file directives name them. |
| `upstreams` | list of string | Where the block forwards what it is not authoritative for. Example: `8.8.8.8`. |
| `warnings` | list of string | What was read and deliberately not followed — a file import, mostly. A block with one of these has plugins that are not listed here. |
<!-- whoctl:end status -->
