// Package record is the Record kind: the resource records of a zone file, and
// the one thing in this provider that writes.
//
// # What it may change, and what it may not
//
// A zone file is somebody's, and most of it was written by hand. whoctl owns
// exactly what is between its markers:
//
//	; whoctl:begin whoctl
//	host1.example.com.	IN	A	192.0.2.10
//	; whoctl:end whoctl
//
// Records inside the region are created, updated and deleted. Records outside
// it are listed — a listing that showed only whoctl's own would be a listing
// nobody could trust — and every attempt to change one is refused, saying so.
// That is what makes writing to a file a DNS server is reading acceptable at
// all: the ownership is in the file, where a person can see it, rather than in
// something whoctl remembers.
package record

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
	"github.com/whoctl/whoctl-provider-coredns/internal/zonefile"
	"github.com/whoctl/whoctl-provider-coredns/internal/zones"
)

// Spec is the record as it is asked for.
type Spec struct {
	Zone   string   `yaml:"zone" json:"zone" doc:"The zone that holds it, as the Corefile names it." docExample:"example.com."`
	Name   string   `yaml:"name" json:"name" doc:"The owner name, relative to the zone or absolute. @ is the zone itself." docExample:"host1"`
	Type   string   `yaml:"type" json:"type" doc:"The record type: A, AAAA, CNAME, TXT, MX, and whatever else the zone holds." docExample:"A"`
	Values []string `yaml:"values" json:"values" doc:"The rdata, one entry per record. Two values of one name and type is a round robin: one object here, two lines in the file." docExample:"192.0.2.10"`
	TTL    uint32   `yaml:"ttl,omitempty" json:"ttl,omitempty" doc:"Seconds. Omitted takes the zone's own default, which is what a hand-written record usually does." docExample:"3600"`
	Region string   `yaml:"region,omitempty" json:"region,omitempty" doc:"Which marked region of the file this belongs to. Empty is whoctl's own; a second writer names its own, so that pruning one never reaches the other's." docExample:"leases-to-dns"`
}

// Status is where it lives.
type Status struct {
	FQDN    string `yaml:"fqdn" json:"fqdn" doc:"The owner name, absolute, which is what the file actually holds." docExample:"host1.example.com."`
	File    string `yaml:"file" json:"file" doc:"The zone file it is in." docExample:"/etc/coredns/db.example.com"`
	Managed bool   `yaml:"managed" json:"managed" doc:"Whether it sits inside a whoctl region. A record that does not is read here and refused every change." docExample:"true"`
	Region  string `yaml:"region,omitempty" json:"region,omitempty" doc:"The region it was found in, when it is in one."`
}

// Handler serves the kind.
type Handler struct{ p *provider.Provider }

// New builds the handler.
func New(p *provider.Provider) core.Handler { return &Handler{p: p} }

func (h *Handler) Type() core.ResourceType { return resourceType() }

func resourceType() core.ResourceType {
	return provider.ResourceType(core.ResourceType{
		Kind:       "Record",
		Plural:     "records",
		Singular:   "record",
		ShortNames: []string{"rr"},
		// The zone is the container and not the namespace. A namespace means
		// one thing across every kind of a provider, and a Kubernetes namespace
		// may not hold a dot — `1.168.192.in-addr.arpa` could not be one
		// without being mangled into something nobody would type. So the zone
		// travels in the spec, where --field-selector spec.zone= reaches it,
		// which is the same answer the aws provider gives for a hosted zone.
		Namespaced:  false,
		Categories:  []string{"dns"},
		Description: "A resource record in a zone file: the one thing this provider writes.",
		Columns: []core.Column{
			{Name: "NAME", Path: "metadata.name"},
			{Name: "ZONE", Path: "spec.zone"},
			{Name: "TYPE", Path: "spec.type"},
			{Name: "VALUES", Path: "spec.values", Format: core.FormatFirst},
			{Name: "TTL", Path: "spec.ttl"},
			{Name: "MANAGED", Path: "status.managed"},
			// No AGE: a zone file records no time per record, and the file's
			// own is the zone's. A column drawn from it would say the same
			// thing for every row.
			{Name: "FQDN", Wide: true, Path: "status.fqdn"},
			{Name: "REGION", Wide: true, Path: "status.region"},
		},
	})
}

func (h *Handler) NewSpec() any { return &Spec{} }

func (h *Handler) NewStatus() any { return &Status{} }

// List reads every record of every zone the Corefile loads.
func (h *Handler) List(_ context.Context) ([]core.Object, error) {
	loaded, err := h.zones()
	if err != nil {
		return nil, err
	}
	var out []core.Object
	for _, z := range loaded {
		objs, err := h.inZone(z)
		if err != nil {
			// A zone whose file is missing is reported by the Zone kind, which
			// is where that belongs. Here it is a zone with no records.
			continue
		}
		out = append(out, objs...)
	}
	return out, nil
}

// Get reads one record by the name a listing prints, or by its fully qualified
// name when only one type answers for it.
func (h *Handler) Get(_ context.Context, name string) (core.Object, error) {
	loaded, err := h.zones()
	if err != nil {
		return core.Object{}, err
	}
	var matches []core.Object
	for _, z := range loaded {
		objs, err := h.inZone(z)
		if err != nil {
			continue
		}
		for _, obj := range objs {
			if matchesName(obj, name) {
				matches = append(matches, obj)
			}
		}
	}
	switch len(matches) {
	case 0:
		return core.Object{}, core.NotFound("record", name)
	case 1:
		return matches[0], nil
	default:
		return core.Object{}, core.Invalidf("%q names %d records: ask for one of %s",
			name, len(matches), strings.Join(namesOf(matches), ", "))
	}
}

// Apply writes the record into its zone's marked region.
func (h *Handler) Apply(_ context.Context, obj core.Object) (core.Result, error) {
	spec, ok := obj.Spec.(*Spec)
	if !ok || spec == nil {
		return core.Result{}, core.Invalidf("no spec: a record needs a zone, a name, a type and values")
	}
	if err := spec.check(); err != nil {
		return core.Result{}, err
	}

	z, src, err := h.zoneOf(spec.Zone)
	if err != nil {
		return core.Result{}, err
	}
	fqdn := absolute(spec.Name, z.Origin)
	region := regionOf(spec.Region)

	if err := refuseIfOutside(src, region, fqdn, spec.Type); err != nil {
		return core.Result{}, err
	}

	body, _ := zonefile.Region(src, region)
	next, action := replaceIn(body, fqdn, spec.Type, render(fqdn, spec))
	result := core.Result{Action: action, Object: object(z, fqdn, spec, region, true)}
	if action == core.ActionUnchanged {
		return result, nil
	}

	out, serial, err := zonefile.BumpSerial(zonefile.SetRegion(src, region, next))
	if err != nil {
		return core.Result{}, core.Invalidf("cannot write to %s: %v", z.Path, err)
	}
	if err := h.write(z.Path, out); err != nil {
		return core.Result{}, err
	}
	result.Diff = []string{fmt.Sprintf("%s in %s, serial now %d", fqdn, z.File, serial)}
	return result, nil
}

// Delete removes a record from its region.
//
// Only from a region: a record somebody wrote by hand is refused, with the file
// and the line in the message. Deleting is the one verb whose mistake cannot be
// undone by running the command again, and the marked region is what makes it
// possible to be sure whose record this is.
func (h *Handler) Delete(ctx context.Context, name string) error {
	obj, err := h.Get(ctx, name)
	if err != nil {
		return err
	}
	spec := obj.Spec.(*Spec)
	status := obj.Status.(*Status)
	if !status.Managed {
		return core.Refusedf(
			"%s is not in a whoctl region of %s: it was written by hand, and this provider only removes what it wrote",
			name, status.File)
	}

	z, src, err := h.zoneOf(spec.Zone)
	if err != nil {
		return err
	}
	region := regionOf(status.Region)
	body, _ := zonefile.Region(src, region)
	next, action := replaceIn(body, status.FQDN, spec.Type, "")
	if action == core.ActionUnchanged {
		return core.NotFound("record", name)
	}

	out, _, err := zonefile.BumpSerial(zonefile.SetRegion(src, region, next))
	if err != nil {
		return core.Invalidf("cannot write to %s: %v", z.Path, err)
	}
	return h.write(z.Path, out)
}

// check is what a manifest has to carry.
//
// Nothing here validates rdata against its type: that is a resolver's job, and
// being wrong about it would be worse than not answering. CoreDNS refuses to
// load a zone it cannot parse, which is a check by something that knows.
func (s *Spec) check() error {
	switch {
	case strings.TrimSpace(s.Zone) == "":
		return core.Invalidf("no zone: a record has to say which zone holds it")
	case strings.TrimSpace(s.Name) == "":
		return core.Invalidf("no name: use @ for the zone itself")
	case strings.TrimSpace(s.Type) == "":
		return core.Invalidf("no type")
	case len(s.Values) == 0:
		return core.Invalidf("no values: a record with no rdata is not a record")
	}
	for _, v := range s.Values {
		if strings.TrimSpace(v) == "" {
			return core.Invalidf("an empty value")
		}
	}
	if strings.EqualFold(s.Type, "SOA") {
		// The serial is maintained by this provider on every write, so a
		// managed SOA would be whoctl arguing with itself.
		return core.Unsupportedf("the SOA is the zone's own, and this provider rewrites its serial on every change")
	}
	return nil
}

func (h *Handler) zones() ([]zones.Loaded, error) {
	f, err := h.p.LoadCorefile()
	if err != nil {
		return nil, err
	}
	return zones.Discover(h.p, f), nil
}

// zoneOf finds the zone a spec names and reads its file.
func (h *Handler) zoneOf(name string) (zones.Loaded, string, error) {
	loaded, err := h.zones()
	if err != nil {
		return zones.Loaded{}, "", err
	}
	matches := zones.Matching(loaded, name)
	switch len(matches) {
	case 1:
	case 0:
		return zones.Loaded{}, "", core.Invalidf("no zone %q is loaded here: the Corefile loads %s",
			name, strings.Join(zoneNames(loaded), ", "))
	default:
		// CoreDNS allows it and a writer cannot resolve it: two files for one
		// origin means two answers to "where does this record go", and picking
		// one would be picking wrong half the time.
		return zones.Loaded{}, "", core.Invalidf(
			"the Corefile loads %s from %d different files (%s), so there is no single file to write this record to",
			name, len(matches), strings.Join(fileNames(matches), ", "))
	}
	z := matches[0]
	src, err := os.ReadFile(z.Path)
	if err != nil {
		return zones.Loaded{}, "", core.Unavailablef("cannot read %s: %v", z.Path, err)
	}
	return z, string(src), nil
}

// write replaces the file, keeping its mode.
//
// Through a temporary file in the same directory and a rename, because CoreDNS
// is reading this file on a timer: a truncate-and-write leaves a window where
// the zone is half a file, and a reload landing in it drops the zone.
func (h *Handler) write(path, content string) error {
	if h.p.Runner != nil && h.p.Runner.DryRun {
		return nil
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".whoctl-*")
	if err != nil {
		return core.Unavailablef("cannot write beside %s: %v", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return core.Internalf("writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return core.Internalf("closing %s: %w", name, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return core.Internalf("setting the mode of %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return core.Unavailablef("cannot replace %s: %v", path, err)
	}
	return nil
}
