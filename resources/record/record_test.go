package record

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/sysexec"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
	"github.com/whoctl/whoctl-provider-coredns/internal/zonefile"
)

// zoneCopy gives each test its own tree to write into. Nothing here may touch
// testdata/: a suite that edits its own fixtures passes once and then tests
// whatever it left behind.
func zoneCopy(t *testing.T) (core.Handler, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "coredns")
	if err := os.MkdirAll(filepath.Join(dir, "zones"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Corefile", "db.example.com", "db.example.org"} {
		copyFile(t, filepath.Join("../../testdata/etc/coredns", name), filepath.Join(dir, name))
	}
	copyFile(t, "../../testdata/etc/coredns/zones/db.internal.test", filepath.Join(dir, "zones", "db.internal.test"))

	p := provider.New(provider.Options{Root: root, Corefile: filepath.Join(dir, "Corefile")})
	return New(p), filepath.Join(dir, "db.example.com")
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func apply(t *testing.T, h core.Handler, spec *Spec) core.Result {
	t.Helper()
	res, err := h.Apply(context.Background(), core.Object{Spec: spec})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func host1(zone string) *Spec {
	return &Spec{Zone: zone, Name: "host1", Type: "A", Values: []string{"192.0.2.10"}}
}

// Everything in the file is listed, whoctl's and everybody else's. A listing of
// only what whoctl wrote would be one nobody could trust to say what the zone
// holds.
func TestListReadsTheWholeZone(t *testing.T) {
	h, _ := zoneCopy(t)
	objs, err := h.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, obj := range objs {
		names[obj.Metadata.Name] = true
		if obj.Status.(*Status).Managed {
			t.Errorf("%s is not in a region and was reported as managed", obj.Metadata.Name)
		}
	}
	for _, want := range []string{"a-ns1.example.com", "cname-www.example.com", "txt-_dmarc.example.com"} {
		if !names[want] {
			t.Errorf("%s is missing from the listing", want)
		}
	}
	// The SOA is the zone's own, reported by the Zone kind.
	if names["soa-example.com"] {
		t.Error("the SOA was listed as a record")
	}
}

// A round robin is one thing somebody manages and two lines in the file.
func TestOneNameAndTypeIsOneObject(t *testing.T) {
	h, _ := zoneCopy(t)
	obj, err := h.Get(context.Background(), "ns-example.com")
	if err != nil {
		t.Fatal(err)
	}
	spec := obj.Spec.(*Spec)
	if len(spec.Values) != 2 {
		t.Fatalf("values = %v, want both apex name servers in one object", spec.Values)
	}
	if spec.Name != "@" {
		t.Errorf("name = %q, want the apex spelled as somebody would write it", spec.Name)
	}
}

// The promise of the marked region, tested on the file this provider is most
// likely to be pointed at.
func TestApplyLeavesEverythingElseAlone(t *testing.T) {
	h, path := zoneCopy(t)
	before := read(t, path)

	if got := apply(t, h, host1("example.com.")).Action; got != core.ActionCreated {
		t.Errorf("action = %q, want created", got)
	}
	after := read(t, path)

	for _, line := range strings.Split(before, "\n") {
		if strings.TrimSpace(line) == "" || strings.Contains(line, "2026080501") {
			continue // the serial is the one line that must change
		}
		if !strings.Contains(after, line) {
			t.Errorf("line lost or rewritten: %q", line)
		}
	}
	if !strings.Contains(after, "        2026080502 ; serial") {
		t.Error("the serial was not bumped, or the SOA was reformatted")
	}
	if !strings.Contains(after, "host1.example.com.\tIN\tA\t192.0.2.10") {
		t.Errorf("the record is not in the file:\n%s", after)
	}
}

// A second pass of an adapter is the ordinary case. It must not write, and it
// must not bump the serial: a serial that moves every two minutes tells every
// secondary in the world to transfer a zone that did not change.
func TestApplyingTwiceChangesNothing(t *testing.T) {
	h, path := zoneCopy(t)
	apply(t, h, host1("example.com."))
	once := read(t, path)

	res := apply(t, h, host1("example.com."))
	if res.Action != core.ActionUnchanged {
		t.Errorf("action = %q, want unchanged", res.Action)
	}
	if read(t, path) != once {
		t.Error("the second apply rewrote the file")
	}
}

func TestChangingAValueIsConfigured(t *testing.T) {
	h, path := zoneCopy(t)
	apply(t, h, host1("example.com."))

	changed := host1("example.com.")
	changed.Values = []string{"192.0.2.11"}
	if got := apply(t, h, changed).Action; got != core.ActionConfigured {
		t.Errorf("action = %q, want configured", got)
	}
	after := read(t, path)
	if strings.Contains(after, "192.0.2.10") {
		t.Error("the old value is still there")
	}
	if !strings.Contains(after, "        2026080503 ; serial") {
		t.Error("the serial did not move with the change")
	}
}

// The whole safety property: a record somebody wrote by hand is not whoctl's to
// edit, however much it looks like one it would have written.
func TestApplyRefusesARecordItDidNotWrite(t *testing.T) {
	h, path := zoneCopy(t)
	before := read(t, path)

	_, err := h.Apply(context.Background(), core.Object{
		Spec: &Spec{Zone: "example.com.", Name: "ns1", Type: "A", Values: []string{"192.0.2.99"}},
	})
	if core.CodeOf(err) != core.CodeRefused {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("the refusal must say where it is: %v", err)
	}
	if read(t, path) != before {
		t.Error("the file was changed by a refused apply")
	}
}

func TestDeleteRemovesOnlyWhatItWrote(t *testing.T) {
	h, path := zoneCopy(t)
	apply(t, h, host1("example.com."))

	if err := h.Delete(context.Background(), "a-host1.example.com"); err != nil {
		t.Fatal(err)
	}
	after := read(t, path)
	if strings.Contains(after, "host1.example.com.") {
		t.Error("the record is still in the file")
	}
	if !strings.Contains(after, zonefile.Begin(zonefile.DefaultRegion)) {
		t.Error("the markers went with the record")
	}
	if !strings.Contains(after, "        2026080503 ; serial") {
		t.Error("the serial did not move with the deletion")
	}
}

func TestDeleteRefusesAHandWrittenRecord(t *testing.T) {
	h, path := zoneCopy(t)
	before := read(t, path)

	err := h.Delete(context.Background(), "a-ns1.example.com")
	if core.CodeOf(err) != core.CodeRefused {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if read(t, path) != before {
		t.Error("a refused delete changed the file")
	}
}

func TestDeleteIsNotFoundRatherThanEmpty(t *testing.T) {
	h, _ := zoneCopy(t)
	if err := h.Delete(context.Background(), "a-nothing.example.com"); !core.IsNotFound(err) {
		t.Errorf("err = %v, want a not-found", err)
	}
}

// Two writers, two regions. Pruning one may not reach the other's records —
// which is the property an adapter's prune rests on.
func TestRegionsAreIndependent(t *testing.T) {
	h, path := zoneCopy(t)

	leases := &Spec{Zone: "example.com.", Name: "tv", Type: "A", Values: []string{"192.0.2.20"}, Region: "leases-to-dns"}
	apply(t, h, leases)
	apply(t, h, host1("example.com."))

	if err := h.Delete(context.Background(), "a-host1.example.com"); err != nil {
		t.Fatal(err)
	}
	after := read(t, path)
	if !strings.Contains(after, "tv.example.com.") {
		t.Error("deleting from one region took the other's record with it")
	}
	if !strings.Contains(after, zonefile.Begin("leases-to-dns")) {
		t.Error("the other region's markers are gone")
	}
}

// A record in a named region reports it, so that something pruning by region
// can tell its own from everybody else's.
func TestAManagedRecordSaysWhichRegionItIsIn(t *testing.T) {
	h, _ := zoneCopy(t)
	apply(t, h, &Spec{Zone: "example.com.", Name: "tv", Type: "A", Values: []string{"192.0.2.20"}, Region: "leases-to-dns"})

	obj, err := h.Get(context.Background(), "a-tv.example.com")
	if err != nil {
		t.Fatal(err)
	}
	status := obj.Status.(*Status)
	if !status.Managed || status.Region != "leases-to-dns" {
		t.Errorf("status = %+v, want it managed and in its own region", status)
	}
}

func TestGetTakesTheSpellingsSomebodyHas(t *testing.T) {
	h, _ := zoneCopy(t)
	for _, want := range []string{"a-ns1.example.com", "ns1.example.com", "ns1.example.com.", "ns1.example.com A", "ns1.example.com/A"} {
		if _, err := h.Get(context.Background(), want); err != nil {
			t.Errorf("Get(%q): %v", want, err)
		}
	}
}

// A name answering for two types has to be told apart, not guessed at.
func TestAnAmbiguousNameNamesBoth(t *testing.T) {
	h, _ := zoneCopy(t)
	apply(t, h, &Spec{Zone: "example.com.", Name: "dual", Type: "A", Values: []string{"192.0.2.30"}})
	apply(t, h, &Spec{Zone: "example.com.", Name: "dual", Type: "TXT", Values: []string{`"hello"`}})

	_, err := h.Get(context.Background(), "dual.example.com")
	if err == nil || !strings.Contains(err.Error(), "txt-dual.example.com") {
		t.Errorf("err = %v, want both names", err)
	}
}

func TestApplyRefusesAZoneThatIsNotLoaded(t *testing.T) {
	h, _ := zoneCopy(t)
	_, err := h.Apply(context.Background(), core.Object{Spec: host1("nowhere.invalid.")})
	if core.CodeOf(err) != core.CodeInvalid || !strings.Contains(err.Error(), "example.com.") {
		t.Errorf("err = %v, want an invalid naming the zones that are loaded", err)
	}
}

func TestApplyRefusesTheSOA(t *testing.T) {
	h, _ := zoneCopy(t)
	_, err := h.Apply(context.Background(), core.Object{
		Spec: &Spec{Zone: "example.com.", Name: "@", Type: "SOA", Values: []string{"ns1.example.com. hostmaster.example.com. 1 2 3 4 5"}},
	})
	if core.CodeOf(err) != core.CodeUnsupported {
		t.Errorf("err = %v, want unsupported", err)
	}
}

// --dry-run says what it would do and writes nothing. For the one kind here
// that writes, that is the flag people will reach for first.
func TestDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "coredns")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Corefile", "db.example.com", "db.example.org"} {
		copyFile(t, filepath.Join("../../testdata/etc/coredns", name), filepath.Join(dir, name))
	}
	if err := os.MkdirAll(filepath.Join(dir, "zones"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "../../testdata/etc/coredns/zones/db.internal.test", filepath.Join(dir, "zones", "db.internal.test"))

	path := filepath.Join(dir, "db.example.com")
	before := read(t, path)
	h := New(provider.New(provider.Options{
		Root:     root,
		Corefile: filepath.Join(dir, "Corefile"),
		Runner:   &sysexec.Runner{DryRun: true},
	}))

	if got := apply(t, h, host1("example.com.")).Action; got != core.ActionCreated {
		t.Errorf("action = %q, want it to report what it would do", got)
	}
	if read(t, path) != before {
		t.Error("--dry-run wrote to the file")
	}
}

// What whoctl writes has to be readable by the thing that reads zone files —
// starting with this provider's own parser, which is what a listing goes
// through.
func TestWhatItWritesItCanReadBack(t *testing.T) {
	h, path := zoneCopy(t)
	apply(t, h, &Spec{Zone: "example.com.", Name: "ttl-host", Type: "A", Values: []string{"192.0.2.40"}, TTL: 600})

	parsed, err := zonefile.ParseFile(path, "example.com.")
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range parsed.Records {
		if rec.Name == "ttl-host.example.com." {
			if rec.TTL != 600 || rec.Type != "A" || strings.Join(rec.Data, " ") != "192.0.2.40" {
				t.Errorf("read back as %+v", rec)
			}
			return
		}
	}
	t.Errorf("the record was written and cannot be read back:\n%s", read(t, path))
}
