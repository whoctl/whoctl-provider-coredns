package server

import (
	"context"
	"strings"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
)

// handler answers from the fixture tree, which is a Corefile and the zone files
// it points at. Nothing here reads the machine's own CoreDNS.
func handler(t *testing.T) core.Handler {
	t.Helper()
	return New(provider.New(provider.Options{
		Root:     "../../testdata",
		Corefile: "../../testdata/etc/coredns/Corefile",
	}))
}

func list(t *testing.T) []core.Object {
	t.Helper()
	objs, err := handler(t).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return objs
}

func TestListReadsEveryBlockAndNoSnippet(t *testing.T) {
	var names []string
	for _, o := range list(t) {
		names = append(names, o.Metadata.Name)
	}
	want := "example.com-53 internal.test-5353 root-53"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("servers = %q, want %q", got, want)
	}
}

func TestAServerCarriesItsChainAndItsZones(t *testing.T) {
	objs := list(t)
	spec := objs[0].Spec.(*Spec)
	status := objs[0].Status.(*Status)

	if len(spec.Zones) != 2 || spec.Zones[0] != "example.com." || spec.Zones[1] != "example.org." {
		t.Errorf("zones = %v", spec.Zones)
	}
	if spec.Port != "53" {
		t.Errorf("port = %q", spec.Port)
	}
	// log and errors come from the snippet it imports, so five is the count
	// that proves the expansion reached the object and not only the parser.
	if status.PluginCount != 5 || len(spec.Plugins) != 5 {
		var names []string
		for _, p := range spec.Plugins {
			names = append(names, p.Name)
		}
		t.Errorf("plugins = %v, want the chain with the snippet expanded", names)
	}
	if len(status.ZoneFiles) != 2 {
		t.Errorf("zoneFiles = %v, want both", status.ZoneFiles)
	}
	if status.Line != 12 {
		t.Errorf("line = %d, want where the block opens", status.Line)
	}
}

// A block with a nested plugin block says so without pretending to model what
// is inside it.
func TestAPluginBlockIsFlaggedNotModelled(t *testing.T) {
	for _, o := range list(t) {
		if o.Metadata.Name != "root-53" {
			continue
		}
		status := o.Status.(*Status)
		if len(status.Upstreams) != 2 || status.Upstreams[0] != "1.1.1.1" {
			t.Errorf("upstreams = %v, want both forwarders", status.Upstreams)
		}
		for _, p := range o.Spec.(*Spec).Plugins {
			if p.Name == "forward" && !p.Block {
				t.Error("forward opened a block and the object does not say so")
			}
		}
		return
	}
	t.Fatal("root-53 is missing")
}

// AGE has to have something to render, or the column is blank in every client.
func TestAServerIsStampedWithTheCorefilesTime(t *testing.T) {
	for _, o := range list(t) {
		if o.Metadata.CreationTimestamp.IsZero() {
			t.Errorf("%s has no timestamp: the AGE column would be blank", o.Metadata.Name)
		}
		if o.Metadata.UID == "" {
			t.Errorf("%s has no uid", o.Metadata.Name)
		}
	}
}

// The name carries a port nobody types, so a zone finds its block.
func TestGetAcceptsAZoneAsWellAsAName(t *testing.T) {
	h := handler(t)
	for _, name := range []string{"example.com-53", "example.com", "example.com.", "example.org", "example.org:53"} {
		obj, err := h.Get(context.Background(), name)
		if err != nil {
			t.Errorf("Get(%q): %v", name, err)
			continue
		}
		if obj.Metadata.Name != "example.com-53" {
			t.Errorf("Get(%q) = %q", name, obj.Metadata.Name)
		}
	}
	if _, err := h.Get(context.Background(), "nowhere.test"); !core.IsNotFound(err) {
		t.Errorf("Get(nowhere.test) = %v, want not found", err)
	}
}

// A Corefile that is not there is the ordinary case on a laptop, and the answer
// has to name the path rather than just failing.
func TestAMissingCorefileSaysWhereItLooked(t *testing.T) {
	h := New(provider.New(provider.Options{Corefile: "../../testdata/nothing/Corefile"}))
	_, err := h.List(context.Background())
	if err == nil {
		t.Fatal("a missing Corefile listed successfully")
	}
	if !strings.Contains(err.Error(), "testdata/nothing/Corefile") {
		t.Errorf("error = %q, want the path in it", err)
	}
}

// The kind publishes what it can do, so a Kubernetes client greys out the rest
// instead of offering an edit that fails.
func TestItSaysItIsReadOnly(t *testing.T) {
	h := handler(t)
	if verbs := h.Type().Verbs; len(verbs) != 2 {
		t.Errorf("verbs = %v, want get and list only", verbs)
	}
	if _, err := h.Apply(context.Background(), core.Object{}); err == nil {
		t.Error("Apply succeeded on a read-only kind")
	}
	if err := h.Delete(context.Background(), "example.com-53"); err == nil {
		t.Error("Delete succeeded on a read-only kind")
	}
}
