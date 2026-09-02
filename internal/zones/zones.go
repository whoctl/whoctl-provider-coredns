// Package zones is the discovery both zone-shaped kinds share: which files this
// CoreDNS loads, for which origins, from which server blocks.
//
// It exists because a Zone and a Record ask the same question of the Corefile
// and would otherwise answer it twice — and two answers to "which file is
// example.com. served from" is how a record gets written into a file nobody is
// serving.
package zones

import (
	"sort"
	"strings"

	"github.com/whoctl/whoctl-provider-coredns/internal/corefile"
	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
)

// Loaded is one origin, and everything the Corefile says about it.
type Loaded struct {
	Origin  string // absolute, with the trailing dot
	File    string // as the Corefile writes it
	Path    string // resolved
	Servers []string
}

// Name is the origin as an object name: the trailing dot goes, because a
// Kubernetes name may not end in one, and the root zone is spelled the way the
// server block that serves it is.
func (l Loaded) Name() string { return Name(l.Origin) }

// Name turns an origin into the name an object carries.
func Name(origin string) string {
	name := strings.ToLower(strings.TrimSuffix(origin, "."))
	if name == "" {
		return "root"
	}
	return corefile.SanitizeName(name)
}

// Discover walks the server blocks and collects the zone files they load.
//
// The same file loaded by two blocks is one zone with two servers, which is the
// ordinary case — a Corefile listening on 53 and on 5353 writes the `file`
// directive twice. Two *different* files for one origin is not ordinary, and
// stays two entries rather than being merged into one that is neither.
func Discover(p *provider.Provider, f *corefile.File) []Loaded {
	index := map[string]*Loaded{}
	var order []string

	for _, srv := range f.Servers {
		name := corefile.ServerName(srv)
		root := srv.Root()
		for _, d := range srv.DirectivesNamed("file") {
			if len(d.Args) == 0 {
				continue
			}
			for _, origin := range srv.FileZones(d) {
				key := origin + "\x00" + d.Args[0]
				z, seen := index[key]
				if !seen {
					z = &Loaded{Origin: origin, File: d.Args[0], Path: p.Resolve(d.Args[0], root)}
					index[key] = z
					order = append(order, key)
				}
				z.Servers = append(z.Servers, name)
			}
		}
	}

	out := make([]Loaded, 0, len(order))
	for _, key := range order {
		z := *index[key]
		sort.Strings(z.Servers)
		out = append(out, z)
	}
	return out
}

// Find looks a zone up the way somebody would name it: by the object name, by
// the origin with or without its trailing dot.
//
// An origin served from two different files is two entries and is not found
// this way — which is deliberate for anything that writes: there is no single
// file to write to, and picking one would be picking wrong half the time.
// Matching is what says which of the two happened.
func Find(all []Loaded, want string) (Loaded, bool) {
	matches := Matching(all, want)
	if len(matches) != 1 {
		return Loaded{}, false
	}
	return matches[0], true
}

// Matching is every zone answering to a name.
//
// More than one means the Corefile loads that origin from several files, which
// CoreDNS allows and a writer cannot resolve. The caller needs the list to say
// so: an error naming the origin twice explains nothing, and that is what it
// said before this existed.
func Matching(all []Loaded, want string) []Loaded {
	want = strings.ToLower(strings.TrimSpace(want))
	bare := strings.TrimSuffix(want, ".")

	var out []Loaded
	for _, z := range all {
		if z.Name() == Name(want) || strings.TrimSuffix(strings.ToLower(z.Origin), ".") == bare {
			out = append(out, z)
		}
	}
	return out
}
