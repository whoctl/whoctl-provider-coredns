// Package zone is the Zone kind: a zone file a server block loads.
//
// A zone is not a thing the Corefile contains — it is a file the Corefile
// points at, through a `file` directive. So this kind is discovered rather than
// declared: reading it means reading the Corefile to find out which files are
// loaded for which origins, and then reading those.
//
// Only zones reached through `file` are here. The `auto` plugin loads a whole
// directory by pattern, and `kubernetes`, `etcd` and `route53` answer from
// somewhere that is not a file at all — none of those is a zone file, and
// inventing an object for them would be describing something this provider
// cannot read.
package zone

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
	"github.com/whoctl/whoctl-provider-coredns/internal/zonefile"
	"github.com/whoctl/whoctl-provider-coredns/internal/zones"
)

// Spec is the zone as the Corefile asks for it.
type Spec struct {
	Origin string `yaml:"origin" json:"origin" doc:"The zone's own name, absolute." docExample:"example.com."`
	File   string `yaml:"file" json:"file" doc:"The zone file, exactly as the Corefile's file directive names it." docExample:"db.example.com"`
	TTL    uint32 `yaml:"ttl,omitempty" json:"ttl,omitempty" doc:"The zone's default TTL in seconds, from $TTL. Zero when the file states none." docExample:"3600"`
}

// Status is what the file itself says.
type Status struct {
	Path      string   `yaml:"path" json:"path" doc:"The path actually opened, once the Corefile's own was resolved." docExample:"/etc/coredns/db.example.com"`
	Servers   []string `yaml:"servers" json:"servers" doc:"The server blocks that load this zone. More than one is ordinary: the same file often answers on two ports." docFlags:"readOnly"`
	Serial    uint32   `yaml:"serial,omitempty" json:"serial,omitempty" doc:"The SOA serial, which is what a secondary compares to decide whether to transfer." docExample:"2026080501"`
	Refresh   uint32   `yaml:"refresh,omitempty" json:"refresh,omitempty" doc:"SOA refresh, in seconds."`
	Retry     uint32   `yaml:"retry,omitempty" json:"retry,omitempty" doc:"SOA retry, in seconds."`
	Expire    uint32   `yaml:"expire,omitempty" json:"expire,omitempty" doc:"SOA expire, in seconds."`
	Minimum   uint32   `yaml:"minimum,omitempty" json:"minimum,omitempty" doc:"SOA minimum, which is the negative-answer TTL."`
	PrimaryNS string   `yaml:"primaryNameServer,omitempty" json:"primaryNameServer,omitempty" doc:"The SOA's primary name server." docExample:"ns1.example.com."`
	Mailbox   string   `yaml:"mailbox,omitempty" json:"mailbox,omitempty" doc:"The SOA's responsible mailbox, in DNS spelling — the first dot stands for the @." docExample:"hostmaster.example.com."`

	Records     int            `yaml:"records" json:"records" doc:"How many resource records the file holds."`
	RecordTypes map[string]int `yaml:"recordTypes,omitempty" json:"recordTypes,omitempty" doc:"How many of each type, so a zone can be sized up without listing it."`
	NameServers []string       `yaml:"nameServers,omitempty" json:"nameServers,omitempty" doc:"The NS records at the apex: what the parent delegates to." docFlags:"readOnly"`

	Message string `yaml:"message,omitempty" json:"message,omitempty" doc:"Why the numbers above are missing or short — the file could not be read, or it uses something that was not expanded." docFlags:"readOnly"`
}

// Handler serves the kind.
type Handler struct {
	p *provider.Provider
	provider.ReadOnly
}

// New builds the handler.
func New(p *provider.Provider) core.Handler {
	return &Handler{p: p, ReadOnly: provider.ReadOnly{Kind: "Zone"}}
}

func (h *Handler) Type() core.ResourceType { return resourceType() }

func resourceType() core.ResourceType {
	return provider.ResourceType(core.ResourceType{
		Kind:       "Zone",
		Plural:     "zones",
		Singular:   "zone",
		ShortNames: []string{"dz"},
		// A zone belongs to the CoreDNS this provider reads, and there is no
		// second axis to divide it along. See Server for the same reasoning.
		Namespaced:  false,
		Categories:  []string{"dns"},
		Verbs:       []string{core.VerbGet, core.VerbList},
		Description: "A zone file CoreDNS loads: its SOA, and what it holds.",
		Columns: []core.Column{
			{Name: "NAME", Path: "metadata.name"},
			{Name: "RECORDS", Path: "status.records"},
			{Name: "SERIAL", Path: "status.serial"},
			{Name: "TTL", Path: "spec.ttl"},
			{Name: "SERVERS", Path: "status.servers", Format: core.FormatFirst},
			{Name: "AGE", Path: "metadata.creationTimestamp", Format: core.FormatAge},
			{Name: "FILE", Wide: true, Path: "status.path"},
			{Name: "MESSAGE", Wide: true, Path: "status.message"},
		},
	})
}

func (h *Handler) NewSpec() any { return &Spec{} }

func (h *Handler) NewStatus() any { return &Status{} }

// List reads every zone the Corefile loads.
func (h *Handler) List(_ context.Context) ([]core.Object, error) {
	f, err := h.p.LoadCorefile()
	if err != nil {
		return nil, err
	}
	discovered := zones.Discover(h.p, f)
	out := make([]core.Object, 0, len(discovered))
	for _, z := range discovered {
		out = append(out, h.object(z))
	}
	return out, nil
}

// Get reads one zone, by its name or by its origin with the trailing dot.
func (h *Handler) Get(_ context.Context, name string) (core.Object, error) {
	f, err := h.p.LoadCorefile()
	if err != nil {
		return core.Object{}, err
	}
	want := strings.ToLower(strings.TrimSuffix(name, "."))
	for _, z := range zones.Discover(h.p, f) {
		if zones.Name(z.Origin) == want || strings.TrimSuffix(z.Origin, ".") == want {
			return h.object(z), nil
		}
	}
	return core.Object{}, core.NotFound("zone", name)
}

// object reads the zone file and builds the object.
//
// A file that cannot be read is still an object: the Corefile says CoreDNS
// loads it, and a zone missing from the listing would read as "not configured"
// when what is true is "configured and broken" — which is the more urgent of
// the two and the one worth being able to see.
func (h *Handler) object(z zones.Loaded) core.Object {
	spec := &Spec{Origin: z.Origin, File: z.File}
	status := &Status{Path: z.Path, Servers: z.Servers}

	parsed, err := zonefile.ParseFile(z.Path, z.Origin)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		status.Message = "the Corefile loads this file and it is not there: CoreDNS will not serve the zone"
	case err != nil:
		status.Message = "cannot read it: " + err.Error()
	default:
		spec.Origin = parsed.Origin
		spec.TTL = parsed.TTL
		status.Records = len(parsed.Records)
		status.RecordTypes = parsed.Types()
		status.NameServers = parsed.NameServers()
		if soa := parsed.SOA; soa != nil {
			status.Serial, status.Refresh, status.Retry = soa.Serial, soa.Refresh, soa.Retry
			status.Expire, status.Minimum = soa.Expire, soa.Minimum
			status.PrimaryNS, status.Mailbox = soa.PrimaryNS, soa.Mailbox
		} else {
			status.Message = "no SOA record: a zone without one is not a zone CoreDNS will answer for"
		}
		if len(parsed.Notes) > 0 {
			status.Message = strings.Join(append(parsed.Notes, status.Message), "; ")
		}
	}
	sort.Strings(status.Servers)

	name := zones.Name(spec.Origin)
	t := resourceType()
	return core.Object{
		APIVersion: t.APIVersion(),
		Kind:       t.Kind,
		Metadata: core.Metadata{
			Name: name,
			UID:  name,
			// When the zone file last changed, which for a zone is when its
			// records last changed. See provider.FileTime on why that is the
			// creation timestamp.
			CreationTimestamp: provider.FileTime(z.Path),
		},
		Spec:   spec,
		Status: status,
	}
}
