// Package server is the Server kind: one server block of a Corefile.
//
// A server block is what CoreDNS is made of — a set of zones, a port, and the
// plugin chain that answers for them. It is the unit that can be added, removed
// or reordered without touching anything else, which is what makes it a kind
// rather than the Corefile being one object with everything inside it.
package server

import (
	"context"
	"strings"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/corefile"
	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
)

// Spec is the block as the Corefile declares it.
type Spec struct {
	Zones     []string `yaml:"zones" json:"zones" doc:"The zones the block is authoritative for, normalized the way CoreDNS normalizes them: lowercase and absolute." docExample:"example.com."`
	Port      string   `yaml:"port" json:"port" doc:"The port it listens on, defaulted from the scheme when the label omits it." docExample:"53"`
	Addresses []string `yaml:"addresses" json:"addresses" doc:"The block's labels exactly as written, schemes and CIDR notation included." docExample:"dns://example.com:53"`
	Plugins   []Plugin `yaml:"plugins,omitempty" json:"plugins,omitempty" doc:"The plugin chain, in the order it is written. The order in the file is not the order CoreDNS runs them in — that is fixed at build time — so this is what the file says, not what happens."`
}

// Plugin is one line of the chain.
//
// It is a name and its arguments and nothing more: CoreDNS has more plugins
// than this provider will ever model, and a plugin it does not recognise has to
// survive being read anyway. What a plugin's own block holds is not here — it
// is kept as raw source by the parser, for the rewrite that does not exist yet.
type Plugin struct {
	Name string   `yaml:"name" json:"name" doc:"The plugin's name, as written." docExample:"forward"`
	Args []string `yaml:"args,omitempty" json:"args,omitempty" doc:"Its arguments on the same line."`
	// Block says the plugin opened one without spelling out what is in it,
	// because that is the part this provider deliberately does not model.
	Block bool `yaml:"block,omitempty" json:"block,omitempty" doc:"Whether the plugin opened a block of its own."`
}

// Status is what was observed about the block.
type Status struct {
	Corefile    string   `yaml:"corefile" json:"corefile" doc:"The file it was read from." docExample:"/etc/coredns/Corefile"`
	Line        int      `yaml:"line" json:"line" doc:"The line the block opens on, so an editor can be pointed at it."`
	PluginCount int      `yaml:"pluginCount" json:"pluginCount" doc:"How many plugins the chain has."`
	ZoneFiles   []string `yaml:"zoneFiles,omitempty" json:"zoneFiles,omitempty" doc:"The zone files this block loads, as its file directives name them." docFlags:"readOnly"`
	Upstreams   []string `yaml:"upstreams,omitempty" json:"upstreams,omitempty" doc:"Where the block forwards what it is not authoritative for." docExample:"8.8.8.8"`
	Warnings    []string `yaml:"warnings,omitempty" json:"warnings,omitempty" doc:"What was read and deliberately not followed — a file import, mostly. A block with one of these has plugins that are not listed here." docFlags:"readOnly"`
}

// Handler serves the kind.
type Handler struct {
	p *provider.Provider
	provider.ReadOnly
}

// New builds the handler.
func New(p *provider.Provider) core.Handler {
	return &Handler{p: p, ReadOnly: provider.ReadOnly{Kind: "Server"}}
}

func (h *Handler) Type() core.ResourceType { return resourceType() }

// resourceType has no receiver because building an object needs the apiVersion
// and the kind, and an object is built from a parse where there is no handler
// to hand.
func resourceType() core.ResourceType {
	return provider.ResourceType(core.ResourceType{
		Kind:       "Server",
		Plural:     "servers",
		Singular:   "server",
		ShortNames: []string{"srv"},
		// A Corefile describes one CoreDNS. There is no second axis to divide
		// it along, so nothing here is namespaced and `-n` does nothing —
		// the same way a Node ignores it.
		Namespaced:  false,
		Categories:  []string{"dns"},
		Verbs:       []string{core.VerbGet, core.VerbList},
		Description: "A server block of a Corefile: the zones it answers for, and its plugin chain.",
		Columns: []core.Column{
			{Name: "NAME", Path: "metadata.name"},
			{Name: "ZONES", Path: "spec.zones"},
			{Name: "PORT", Path: "spec.port"},
			{Name: "PLUGINS", Path: "status.pluginCount"},
			{Name: "UPSTREAM", Path: "status.upstreams", Format: core.FormatFirst},
			{Name: "AGE", Path: "metadata.creationTimestamp", Format: core.FormatAge},
			{Name: "LINE", Wide: true, Path: "status.line"},
			{Name: "COREFILE", Wide: true, Path: "status.corefile"},
		},
	})
}

func (h *Handler) NewSpec() any { return &Spec{} }

func (h *Handler) NewStatus() any { return &Status{} }

// List reads every server block in the Corefile.
func (h *Handler) List(_ context.Context) ([]core.Object, error) {
	f, err := h.p.LoadCorefile()
	if err != nil {
		return nil, err
	}
	stamp := provider.FileTime(f.Path)
	out := make([]core.Object, 0, len(f.Servers))
	for _, srv := range f.Servers {
		out = append(out, Object(f, srv, stamp))
	}
	return out, nil
}

// Get reads one block, by its name or by a zone it answers for.
//
// The zone is the convenience, and it is the same one HostedZone offers for a
// domain: the name carries a port that is 53 almost always, and nobody types
// `-53`. When a zone is answered for on two ports the answer is an error naming
// both, rather than whichever came first.
func (h *Handler) Get(_ context.Context, name string) (core.Object, error) {
	f, err := h.p.LoadCorefile()
	if err != nil {
		return core.Object{}, err
	}
	stamp := provider.FileTime(f.Path)

	var byZone []core.Object
	for _, srv := range f.Servers {
		if corefile.ServerName(srv) == name {
			return Object(f, srv, stamp), nil
		}
		if answersFor(srv, name) {
			byZone = append(byZone, Object(f, srv, stamp))
		}
	}
	switch len(byZone) {
	case 0:
		return core.Object{}, core.NotFound("server", name)
	case 1:
		return byZone[0], nil
	default:
		return core.Object{}, core.Invalidf("%q is answered for by %d server blocks: ask for one of %s",
			name, len(byZone), strings.Join(namesOf(byZone), ", "))
	}
}

// answersFor reports whether a block is authoritative for a zone somebody
// named, with or without the trailing dot they will not have typed.
func answersFor(srv *corefile.Server, zone string) bool {
	want := strings.ToLower(strings.TrimSuffix(zone, "."))
	for _, z := range srv.Zones() {
		if strings.TrimSuffix(z, ".") == want {
			return true
		}
	}
	// The label as written, so `example.com:5353` finds its block too.
	for _, label := range srv.Labels {
		if strings.EqualFold(label, zone) {
			return true
		}
	}
	return false
}

// Object builds the object for one block. It is exported because the Zone kind
// needs the same name for a block it is only referring to.
func Object(f *corefile.File, srv *corefile.Server, stamp core.Time) core.Object {
	name := corefile.ServerName(srv)

	spec := &Spec{Zones: srv.Zones(), Port: srv.Port(), Addresses: srv.Labels}
	status := &Status{
		Corefile:    f.Path,
		Line:        srv.Line,
		PluginCount: len(srv.Directives),
		ZoneFiles:   ZoneFiles(srv),
		Upstreams:   upstreamsOf(srv),
	}
	for _, d := range srv.Directives {
		spec.Plugins = append(spec.Plugins, Plugin{Name: d.Name, Args: d.Args, Block: len(d.Body) > 0})
	}
	// A warning about the file as a whole is repeated on every block, because
	// there is no object for the file and saying it nowhere a reader looks is
	// the same as not saying it.
	status.Warnings = append(status.Warnings, f.Warnings...)
	status.Warnings = append(status.Warnings, srv.Warnings...)

	t := resourceType()
	return core.Object{
		APIVersion: t.APIVersion(),
		Kind:       t.Kind,
		Metadata: core.Metadata{
			Name: name,
			// The block's identity is its first zone and its port, and neither
			// changes without the block becoming a different block. That is
			// what a uid is for.
			UID:               name,
			CreationTimestamp: stamp,
			Labels: map[string]string{
				"coredns.whoctl.io/port": srv.Port(),
			},
		},
		Spec:   spec,
		Status: status,
	}
}

// ZoneFiles is the zone files a block loads, as its `file` directives name
// them. It is exported because the Zone kind is built from exactly this.
//
// `file DBFILE [ZONES...]` — when the directive names no zones it serves the
// block's own, which is how nearly every Corefile writes it.
func ZoneFiles(srv *corefile.Server) []string {
	var out []string
	for _, d := range srv.DirectivesNamed("file") {
		if len(d.Args) > 0 {
			out = append(out, d.Args[0])
		}
	}
	return out
}

// upstreamsOf is where the block sends what it cannot answer itself.
//
// `forward` is the plugin every current Corefile uses; `proxy` is what it was
// called before CoreDNS 1.7 and is still in Corefiles that have not been
// touched since. Both spell it `FROM TO...`, so the first argument is the zone
// being forwarded and the rest are the upstreams.
func upstreamsOf(srv *corefile.Server) []string {
	var out []string
	for _, d := range srv.Directives {
		if d.Name != "forward" && d.Name != "proxy" {
			continue
		}
		if len(d.Args) > 1 {
			out = append(out, d.Args[1:]...)
		}
	}
	return out
}

func namesOf(objs []core.Object) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.Metadata.Name)
	}
	return out
}
