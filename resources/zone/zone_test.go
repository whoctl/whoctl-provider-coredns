package zone

import (
	"context"
	"strings"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
)

func handler(t *testing.T) core.Handler {
	t.Helper()
	return New(provider.New(provider.Options{
		Root:     "../../testdata",
		Corefile: "../../testdata/etc/coredns/Corefile",
	}))
}

func list(t *testing.T, h core.Handler) []core.Object {
	t.Helper()
	objs, err := h.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return objs
}

// A zone is discovered by reading the Corefile for `file` directives, so this
// is really a test that the two parsers meet.
func TestListFindsTheZonesTheCorefileLoads(t *testing.T) {
	var names []string
	for _, o := range list(t, handler(t)) {
		names = append(names, o.Metadata.Name)
	}
	want := "example.com example.org internal.test"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("zones = %q, want %q", got, want)
	}
}

func TestAZoneCarriesItsSOAAndItsCounts(t *testing.T) {
	objs := list(t, handler(t))
	spec, status := objs[0].Spec.(*Spec), objs[0].Status.(*Status)

	if spec.Origin != "example.com." || spec.TTL != 3600 {
		t.Errorf("spec = %+v", spec)
	}
	if status.Serial != 2026080501 || status.PrimaryNS != "ns1.example.com." {
		t.Errorf("status = %+v", status)
	}
	if status.Records != 10 {
		t.Errorf("records = %d, want 10", status.Records)
	}
	if status.RecordTypes["A"] != 4 || status.RecordTypes["NS"] != 2 {
		t.Errorf("recordTypes = %v", status.RecordTypes)
	}
	if len(status.NameServers) != 2 {
		t.Errorf("nameServers = %v", status.NameServers)
	}
	if len(status.Servers) != 1 || status.Servers[0] != "example.com-53" {
		t.Errorf("servers = %v, want the block that loads it", status.Servers)
	}
	if status.Message != "" {
		t.Errorf("message = %q, want none for a zone that read cleanly", status.Message)
	}
}

// The `root` directive is what a relative path in that block resolves against,
// and getting it wrong means reading nothing.
func TestTheRootDirectiveIsHonoured(t *testing.T) {
	for _, o := range list(t, handler(t)) {
		if o.Metadata.Name != "internal.test" {
			continue
		}
		status := o.Status.(*Status)
		if !strings.HasSuffix(status.Path, "testdata/etc/coredns/zones/db.internal.test") {
			t.Errorf("path = %q, want it under the block's root", status.Path)
		}
		// The file has no $ORIGIN, so the origin comes from the Corefile.
		if got := o.Spec.(*Spec).Origin; got != "internal.test." {
			t.Errorf("origin = %q, want the one the file directive names", got)
		}
		if status.Records != 5 || status.Serial != 2026080503 {
			t.Errorf("status = %+v", status)
		}
		return
	}
	t.Fatal("internal.test is missing")
}

// A file the Corefile loads and that is not there is configured and broken,
// which is more urgent than not configured — and leaving it out of the listing
// is how the two become indistinguishable.
func TestAZoneWhoseFileIsMissingIsStillListed(t *testing.T) {
	h := New(provider.New(provider.Options{
		Root:     "../../testdata/imports",
		Corefile: "../../testdata/imports/etc/coredns/Corefile",
	}))
	objs := list(t, h)
	if len(objs) != 1 || objs[0].Metadata.Name != "example.test" {
		t.Fatalf("zones = %+v, want the broken one listed", objs)
	}
	status := objs[0].Status.(*Status)
	if status.Records != 0 {
		t.Errorf("records = %d, want none", status.Records)
	}
	if !strings.Contains(status.Message, "not there") {
		t.Errorf("message = %q, want it to say the file is missing", status.Message)
	}
}

func TestGetAcceptsTheOriginWithOrWithoutTheDot(t *testing.T) {
	h := handler(t)
	for _, name := range []string{"example.com", "example.com.", "EXAMPLE.com"} {
		obj, err := h.Get(context.Background(), name)
		if err != nil {
			t.Errorf("Get(%q): %v", name, err)
			continue
		}
		if obj.Metadata.Name != "example.com" {
			t.Errorf("Get(%q) = %q", name, obj.Metadata.Name)
		}
	}
	if _, err := h.Get(context.Background(), "nowhere.test"); !core.IsNotFound(err) {
		t.Errorf("Get(nowhere.test) = %v, want not found", err)
	}
}

func TestItSaysItIsReadOnly(t *testing.T) {
	h := handler(t)
	if verbs := h.Type().Verbs; len(verbs) != 2 {
		t.Errorf("verbs = %v, want get and list only", verbs)
	}
	if _, err := h.Apply(context.Background(), core.Object{}); err == nil {
		t.Error("Apply succeeded on a read-only kind")
	}
	if err := h.Delete(context.Background(), "example.com"); err == nil {
		t.Error("Delete succeeded on a read-only kind")
	}
}
