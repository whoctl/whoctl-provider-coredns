// Package corefile reads a Corefile: the server blocks, their addresses and
// the plugins inside them.
//
// # Why the source is kept
//
// A Corefile is Caddyfile syntax, and CoreDNS has upwards of forty plugins in
// tree and any number out of it. Modelling all of them is not a goal and never
// will be, so this parses the *shape* — blocks, directive names, arguments,
// nesting — and keeps the raw lines of everything it did not interpret.
//
// That is the same bargain the linux provider struck with resolv.conf, for the
// same reason. The day this provider writes a Corefile, the rewrite has to give
// back every plugin, argument and comment it did not understand, and it can
// only do that if it still has them. Parsing into a model that throws the rest
// away is how a DNS server comes back up missing half its configuration.
//
// # What is not done
//
// `import` of a snippet defined in the same file is expanded, because a server
// block that imports one is otherwise reported with plugins it visibly has.
// `import` of a *file* is not: it globs, it recurses, and the result would be a
// listing that no longer corresponds to any file on disk. Those are recorded in
// Warnings instead, so a reader knows the model is short rather than wrong.
package corefile

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
)

// File is a parsed Corefile.
type File struct {
	// Path is where it was read from, for error messages and for status.
	Path string
	// Lines is the source, verbatim and unmodified. Everything that reports a
	// range of raw text slices this.
	Lines []string
	// Servers are the server blocks, in the order they appear.
	Servers []*Server
	// Snippets are the (name) { ... } definitions, by name.
	Snippets map[string]*Snippet
	// Warnings is what was seen and deliberately not modelled. It reaches the
	// objects, because a listing that is quietly short is worse than one that
	// says where it stopped.
	Warnings []string
}

// Snippet is a (name) { ... } definition. It is not a server.
type Snippet struct {
	Name       string
	Directives []Directive
	Line       int
}

// Server is one server block.
type Server struct {
	// Labels are the address tokens on the opening line, exactly as written.
	Labels []string
	// Addresses is those tokens parsed.
	Addresses []Address
	// Directives are the plugins inside the block, in order.
	Directives []Directive
	// Line is the 1-based line the block opens on.
	Line int
	// Body is the raw source between the braces, verbatim.
	Body []string
	// Warnings is what was not modelled inside this block. File-level ones are
	// on File; these belong to one block and travel with its object.
	Warnings []string
}

// Directive is one plugin line, with whatever block it opened.
type Directive struct {
	Name string
	Args []string
	// Body is the raw source of the plugin's own block, verbatim and empty
	// when it opened none.
	Body []string
	// Blocks are that same block parsed one level further, which is as far as
	// anything here needs to look.
	Blocks []Directive
	Line   int
}

// Address is one of a server block's labels, parsed.
type Address struct {
	// Raw is the label as written.
	Raw string
	// Scheme is dns, tls, grpc or https. Empty in the file means dns.
	Scheme string
	// Zone is the zone the block is authoritative for, normalized the way
	// CoreDNS normalizes it: lowercase, with the trailing dot. The root zone
	// is ".".
	Zone string
	// Port is the port it listens on, defaulted from the scheme.
	Port string
}

// Default ports by scheme, as CoreDNS assigns them.
var schemePorts = map[string]string{
	"dns":   "53",
	"tls":   "853",
	"grpc":  "443",
	"https": "443",
}

// ParseFile reads and parses a Corefile.
func ParseFile(path string) (*File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, src)
}

// Parse parses a Corefile that has already been read.
func Parse(path string, src []byte) (*File, error) {
	f := &File{Path: path, Lines: splitLines(string(src)), Snippets: map[string]*Snippet{}}

	for _, st := range statements(lex(string(src))) {
		if len(st.head) == 0 {
			continue
		}
		name := st.head[0].text

		// A snippet is a label in parentheses and nothing else. It defines
		// plugins without serving anything, so it is not a server.
		if inner, ok := snippetName(name); ok && len(st.head) == 1 {
			f.Snippets[inner] = &Snippet{Name: inner, Directives: st.directives(f.Lines), Line: st.line}
			continue
		}

		// A top-level statement with no block is an import of another file, or
		// a global option. Neither is a server, and following an import would
		// produce a listing that matches no file on disk.
		if !st.hasBlock {
			if name == "import" {
				f.Warnings = append(f.Warnings, fmt.Sprintf(
					"line %d: %s is not followed; what it brings in is not listed here", st.line, statementText(st)))
			}
			continue
		}

		srv := &Server{Line: st.line, Body: st.bodyLines(f.Lines)}
		for _, tok := range st.head {
			srv.Labels = append(srv.Labels, tok.text)
			srv.Addresses = append(srv.Addresses, ParseAddress(tok.text))
		}
		srv.Directives = f.expand(st.directives(f.Lines), srv)
		f.Servers = append(f.Servers, srv)
	}
	return f, nil
}

// expand replaces `import <snippet>` with the snippet's own directives, and
// leaves an import of anything else alone.
//
// A server block that imports a snippet has those plugins as surely as if they
// were written out, and reporting the block without them would be reporting
// something that is not running. A file import is the opposite case: what it
// brings in is not in this file, so the honest answer is to say so.
func (f *File) expand(ds []Directive, srv *Server) []Directive {
	var out []Directive
	for _, d := range ds {
		if d.Name != "import" || len(d.Args) == 0 {
			out = append(out, d)
			continue
		}
		snippet, ok := f.Snippets[d.Args[0]]
		if !ok {
			srv.Warnings = append(srv.Warnings, fmt.Sprintf(
				"line %d: import %s is not followed; the plugins it brings in are not listed", d.Line, d.Args[0]))
			out = append(out, d)
			continue
		}
		out = append(out, snippet.Directives...)
	}
	return out
}

// Directive returns the first directive with the given name, and whether there
// was one. A plugin may legitimately appear more than once — `file` for two
// zones is the normal way to write it — so the callers that care use
// Directives.
func (s *Server) Directive(name string) (Directive, bool) {
	for _, d := range s.Directives {
		if d.Name == name {
			return d, true
		}
	}
	return Directive{}, false
}

// DirectivesNamed returns every directive with the given name.
func (s *Server) DirectivesNamed(name string) []Directive {
	var out []Directive
	for _, d := range s.Directives {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// FileZones is the zones one `file` directive loads.
//
// `file DBFILE [ZONES...]` — when the directive names no zones it serves the
// block's own, which is how nearly every Corefile writes it.
func (s *Server) FileZones(d Directive) []string {
	if len(d.Args) > 1 {
		out := make([]string, 0, len(d.Args)-1)
		for _, z := range d.Args[1:] {
			out = append(out, ParseAddress(z).Zone)
		}
		return out
	}
	return s.Zones()
}

// Zones are the zones the block is authoritative for.
func (s *Server) Zones() []string {
	out := make([]string, 0, len(s.Addresses))
	for _, a := range s.Addresses {
		out = append(out, a.Zone)
	}
	return out
}

// Port is the port the block listens on. A block with labels on two different
// ports is not something CoreDNS accepts, so the first is the answer.
func (s *Server) Port() string {
	if len(s.Addresses) == 0 {
		return ""
	}
	return s.Addresses[0].Port
}

// Root is the `root` directive's argument, which is what relative paths inside
// the block are resolved against.
func (s *Server) Root() string {
	if d, ok := s.Directive("root"); ok && len(d.Args) > 0 {
		return d.Args[0]
	}
	return ""
}

// ServerName is a server block's identity.
//
// # Why the first zone and the port
//
// A block can be authoritative for several zones at once — `example.com
// example.org:53 { ... }` is one server — so no single zone is *the* name in
// general. The first one is enough to be unique, though, and not by luck:
// CoreDNS refuses to start when two blocks claim the same zone on the same
// port, so no other block can hold this one's first zone at this port.
//
// The port is in the name because serving the same zone on 53 and on 1053 is
// two blocks and two objects, and it is spelled with a dash rather than a colon
// because a colon is not legal in a Kubernetes object name — which this has to
// be, since these objects are served to kubectl and k9s.
func ServerName(s *Server) string {
	if s == nil || len(s.Addresses) == 0 {
		return "unnamed"
	}
	zone := strings.TrimSuffix(s.Addresses[0].Zone, ".")
	if zone == "" {
		// The root zone is "." and a name may not begin with one. "root" is
		// what it is called out loud, and a literal zone named `root` on the
		// same port would collide — which needs a Corefile serving a
		// single-label internal zone by that exact name.
		zone = "root"
	}
	return SanitizeName(zone + "-" + s.Addresses[0].Port)
}

// ParseAddress reads one of a server block's labels.
func ParseAddress(raw string) Address {
	a := Address{Raw: raw, Scheme: "dns"}
	rest := raw
	if scheme, after, found := strings.Cut(raw, "://"); found {
		a.Scheme = strings.ToLower(scheme)
		rest = after
	}
	a.Port = schemePorts[a.Scheme]

	host := rest
	switch {
	case strings.HasPrefix(rest, "["):
		// A bracketed IPv6 literal, with the port outside the brackets.
		if end := strings.Index(rest, "]"); end >= 0 {
			host = rest[1:end]
			if tail := rest[end+1:]; strings.HasPrefix(tail, ":") {
				a.Port = tail[1:]
			}
		}
	case strings.Count(rest, ":") == 1:
		host, a.Port, _ = strings.Cut(rest, ":")
	}
	// More than one colon and no brackets is a bare IPv6 address, which has no
	// port to split off.

	a.Zone = normalizeZone(host)
	return a
}

// normalizeZone spells a zone the way CoreDNS stores it: lowercase, absolute.
func normalizeZone(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "."
	}
	if strings.Contains(host, "/") {
		if zone, ok := reverseZone(host); ok {
			return zone
		}
		// A prefix that is not on an octet boundary needs the classless
		// delegation CoreDNS builds for it, which is more than one zone. It is
		// left as written rather than reported as one zone that is wrong.
		return host
	}
	if strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

// reverseZone turns an IPv4 CIDR into the in-addr.arpa zone CoreDNS derives
// from it, for the prefixes that are a whole number of octets. Anything else —
// /25, or IPv6 — is left to the caller.
func reverseZone(cidr string) (string, bool) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", false
	}
	ones, _ := network.Mask.Size()
	if ones%8 != 0 || ones == 0 {
		return "", false
	}
	octets := strings.Split(network.IP.To4().String(), ".")
	var reversed []string
	for i := ones/8 - 1; i >= 0; i-- {
		reversed = append(reversed, octets[i])
	}
	return strings.Join(reversed, ".") + ".in-addr.arpa.", true
}

// SanitizeName keeps a name to what a Kubernetes object name may contain,
// because these objects are served to clients that will reject anything else.
// A DNS name is nearly always legal already; an underscore label — `_tcp`,
// which is a real thing to be authoritative for — is the case that is not.
var illegal = regexp.MustCompile(`[^a-z0-9.-]+`)

func SanitizeName(name string) string {
	name = illegal.ReplaceAllString(strings.ToLower(name), "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "unnamed"
	}
	return name
}

func snippetName(tok string) (string, bool) {
	if len(tok) > 2 && strings.HasPrefix(tok, "(") && strings.HasSuffix(tok, ")") {
		return tok[1 : len(tok)-1], true
	}
	return "", false
}

func statementText(st stmt) string {
	parts := make([]string, 0, len(st.head))
	for _, t := range st.head {
		parts = append(parts, t.text)
	}
	return strings.Join(parts, " ")
}

func splitLines(src string) []string {
	return strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
}
