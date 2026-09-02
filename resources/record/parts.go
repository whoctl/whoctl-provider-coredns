package record

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/zonefile"
	"github.com/whoctl/whoctl-provider-coredns/internal/zones"
)

// inZone reads one zone file and groups it into objects.
//
// A group is one owner and one type: `www IN A 192.0.2.1` and `www IN A
// 192.0.2.2` are a round robin, which is one thing somebody manages and two
// lines in the file. Splitting them into two objects would make an apply that
// sets both values an apply that has to delete one of them first.
func (h *Handler) inZone(z zones.Loaded) ([]core.Object, error) {
	raw, err := os.ReadFile(z.Path)
	if err != nil {
		return nil, err
	}
	src := string(raw)
	parsed, err := zonefile.Parse(strings.NewReader(src), z.Origin)
	if err != nil {
		return nil, err
	}

	regions := regionsOf(src)

	type group struct {
		spec   *Spec
		fqdn   string
		region string
		inside bool
	}
	var order []string
	index := map[string]*group{}

	for _, rec := range parsed.Records {
		if strings.EqualFold(rec.Type, "SOA") {
			// The zone's own record, which the Zone kind reports and this kind
			// refuses to manage.
			continue
		}
		region, inside := regionAt(regions, rec.Line)
		key := strings.ToLower(rec.Name) + "\x00" + strings.ToUpper(rec.Type) + "\x00" + region
		g, seen := index[key]
		if !seen {
			g = &group{
				spec:   &Spec{Zone: parsed.Origin, Name: relative(rec.Name, parsed.Origin), Type: strings.ToUpper(rec.Type), TTL: rec.TTL},
				fqdn:   rec.Name,
				region: region,
				inside: inside,
			}
			index[key] = g
			order = append(order, key)
		}
		g.spec.Values = append(g.spec.Values, strings.Join(rec.Data, " "))
	}

	out := make([]core.Object, 0, len(order))
	for _, key := range order {
		g := index[key]
		if g.inside {
			g.spec.Region = g.region
		}
		out = append(out, object(z, g.fqdn, g.spec, g.region, g.inside))
	}
	return out, nil
}

// region is one marked region's line span.
type region struct {
	name       string
	start, end int
}

// regionsOf finds every whoctl region in a file.
//
// Every one of them, and not only the default: a second writer keeps its
// records under its own name, and a listing that only knew about one would
// report the other's as hand-written and refuse to touch them.
func regionsOf(src string) []region {
	var out []region
	for _, line := range strings.Split(src, "\n") {
		text := strings.TrimSpace(line)
		name, ok := strings.CutPrefix(text, "; whoctl:begin ")
		if !ok {
			continue
		}
		if start, end, found := zonefile.RegionBounds(src, name); found {
			out = append(out, region{name: name, start: start, end: end})
		}
	}
	return out
}

func regionAt(regions []region, line int) (string, bool) {
	for _, r := range regions {
		if line > r.start && line < r.end {
			return r.name, true
		}
	}
	return "", false
}

// object builds the object for one group.
func object(z zones.Loaded, fqdn string, spec *Spec, region string, managed bool) core.Object {
	name := objectName(fqdn, spec.Type)
	t := resourceType()
	return core.Object{
		APIVersion: t.APIVersion(),
		Kind:       t.Kind,
		Metadata: core.Metadata{
			Name: name,
			UID:  name,
			// No creationTimestamp: a zone file records no time per record.
		},
		Spec:   spec,
		Status: &Status{FQDN: fqdn, File: z.Path, Managed: managed, Region: region},
	}
}

// objectName is the type and the fully qualified name, which together are what
// identifies a record.
//
// `a-host1.example.com`, not `host1.example.com/A`: a Kubernetes name may hold
// letters, digits, dashes and dots and nothing else, and these objects are
// served to kubectl and k9s. The type goes in front because a name may not
// start with a dash and a type never does.
func objectName(fqdn, rtype string) string {
	return strings.ToLower(strings.ToLower(rtype) + "-" + strings.TrimSuffix(fqdn, "."))
}

// matchesName answers to the object's name, to the fully qualified name, and to
// the name and type spelled apart — because those are what somebody has after
// reading a zone file rather than a listing.
func matchesName(obj core.Object, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	spec := obj.Spec.(*Spec)
	status := obj.Status.(*Status)
	fqdn := strings.TrimSuffix(strings.ToLower(status.FQDN), ".")

	switch strings.ToLower(want) {
	case strings.ToLower(obj.Metadata.Name), fqdn, fqdn + ".":
		return true
	}
	// `www.example.com A` and `www.example.com/A`.
	for _, sep := range []string{" ", "/"} {
		head, tail, ok := strings.Cut(want, sep)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSuffix(head, "."), fqdn) && strings.EqualFold(tail, spec.Type) {
			return true
		}
	}
	return false
}

func namesOf(objs []core.Object) []string {
	out := make([]string, 0, len(objs))
	for _, obj := range objs {
		out = append(out, obj.Metadata.Name)
	}
	sort.Strings(out)
	return out
}

// fileNames is what the ambiguous case has to name: the origins are identical,
// so only the files tell the two apart.
func fileNames(all []zones.Loaded) []string {
	out := make([]string, 0, len(all))
	for _, z := range all {
		out = append(out, z.File)
	}
	return out
}

func zoneNames(all []zones.Loaded) []string {
	out := make([]string, 0, len(all))
	for _, z := range all {
		out = append(out, z.Origin)
	}
	return out
}

// render writes the lines one record becomes.
//
// Absolute names, always. A relative name means whatever the last $ORIGIN in
// the file said, and whoctl's region can be anywhere in it — writing `host1`
// under an $ORIGIN somebody changed later is how a record ends up in a
// different zone than the one it was applied to.
func render(fqdn string, spec *Spec) string {
	var b strings.Builder
	for _, value := range spec.Values {
		b.WriteString(fqdn)
		b.WriteString("\t")
		if spec.TTL > 0 {
			b.WriteString(strconv.FormatUint(uint64(spec.TTL), 10))
			b.WriteString("\t")
		}
		b.WriteString("IN\t")
		b.WriteString(strings.ToUpper(spec.Type))
		b.WriteString("\t")
		b.WriteString(strings.TrimSpace(value))
		b.WriteString("\n")
	}
	return b.String()
}

// replaceIn puts a record's lines into a region body, replacing whatever was
// there for the same name and type, and reports what that did.
//
// An empty body removes the record, which is what Delete passes.
//
// The region comes back sorted, always. Inside its own markers whoctl owns the
// order, and a sorted region means the file a person reviews does not depend on
// which record was applied first — the same reason generated code is formatted
// rather than emitted in the order it was built.
func replaceIn(body, fqdn, rtype, lines string) (string, core.Action) {
	var kept []string
	found := false
	for _, line := range splitLines(body) {
		if ownerAndType(line, fqdn, rtype) {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, splitLines(lines)...)
	sort.Strings(kept)

	next := strings.Join(kept, "\n")
	if next == strings.Join(splitLines(body), "\n") {
		return body, core.ActionUnchanged
	}
	if found {
		return next, core.ActionConfigured
	}
	return next, core.ActionCreated
}

// splitLines is the lines of a region body, blank ones dropped and trailing
// whitespace with them, so that comparing two bodies compares records.
func splitLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// ownerAndType reports whether a rendered line is this record's.
func ownerAndType(line, fqdn, rtype string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return false
	}
	if !strings.EqualFold(strings.TrimSuffix(fields[0], "."), strings.TrimSuffix(fqdn, ".")) {
		return false
	}
	for _, f := range fields[1:] {
		if strings.EqualFold(f, rtype) {
			return true
		}
	}
	return false
}

// refuseIfOutside stops a write that would change a record somebody else wrote.
func refuseIfOutside(src, want, fqdn, rtype string) error {
	parsed, err := zonefile.Parse(strings.NewReader(src), "")
	if err != nil {
		return core.Invalidf("cannot parse the zone: %v", err)
	}
	regions := regionsOf(src)
	for _, rec := range parsed.Records {
		if !strings.EqualFold(rec.Name, fqdn) || !strings.EqualFold(rec.Type, rtype) {
			continue
		}
		if name, inside := regionAt(regions, rec.Line); !inside || name != want {
			return core.Refusedf(
				"%s %s is already in this zone at line %d, outside whoctl's %q region: this provider does not edit what it did not write",
				fqdn, strings.ToUpper(rtype), rec.Line, want)
		}
	}
	return nil
}

// absolute turns an owner name into a fully qualified one.
func absolute(name, origin string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "@":
		return origin
	case strings.HasSuffix(name, "."):
		return strings.ToLower(name)
	}
	return strings.ToLower(name + "." + origin)
}

// relative is the inverse, for a spec that round-trips: a name under the zone
// comes back the way somebody would write it.
func relative(fqdn, origin string) string {
	if strings.EqualFold(fqdn, origin) {
		return "@"
	}
	if suffix := "." + strings.ToLower(origin); strings.HasSuffix(strings.ToLower(fqdn), suffix) {
		return strings.ToLower(strings.TrimSuffix(fqdn, suffix))
	}
	return strings.ToLower(fqdn)
}

func regionOf(name string) string {
	if strings.TrimSpace(name) == "" {
		return zonefile.DefaultRegion
	}
	return strings.TrimSpace(name)
}
