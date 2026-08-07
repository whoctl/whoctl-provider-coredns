package corefile

import (
	"strings"
	"testing"
)

const fixture = `# a comment
(logging) {
    log
    errors
}

example.com example.org:53 {
    import logging
    file db.example.com example.com.
    prometheus :9153   # trailing comment
}

.:53 {
    forward . 1.1.1.1 9.9.9.9 {
        max_concurrent 1000
    }
    cache 30
}
`

func parse(t *testing.T, src string) *File {
	t.Helper()
	f, err := Parse("Corefile", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

// A snippet is not a server, and a Corefile that has one would otherwise be
// reported with a server nobody configured.
func TestSnippetsAreNotServers(t *testing.T) {
	f := parse(t, fixture)
	if len(f.Servers) != 2 {
		var names []string
		for _, s := range f.Servers {
			names = append(names, ServerName(s))
		}
		t.Fatalf("servers = %v, want the two real ones", names)
	}
	if _, ok := f.Snippets["logging"]; !ok {
		t.Errorf("snippets = %v, want logging", f.Snippets)
	}
}

// A block that imports a snippet really does have those plugins, so reporting
// it without them would be reporting something that is not running.
func TestASnippetImportIsExpanded(t *testing.T) {
	f := parse(t, fixture)
	var names []string
	for _, d := range f.Servers[0].Directives {
		names = append(names, d.Name)
	}
	want := "log errors file prometheus"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("plugins = %q, want %q", got, want)
	}
}

// A comment at the end of a line is not an argument.
func TestATrailingCommentIsNotAnArgument(t *testing.T) {
	f := parse(t, fixture)
	d, ok := f.Servers[0].Directive("prometheus")
	if !ok {
		t.Fatal("prometheus is missing")
	}
	if len(d.Args) != 1 || d.Args[0] != ":9153" {
		t.Errorf("args = %v, want just the address", d.Args)
	}
}

// A plugin's own block belongs to that plugin. Without this, `max_concurrent`
// is read as a plugin of the server and the chain is reported two long.
func TestAPluginsBlockDoesNotBecomeAPlugin(t *testing.T) {
	f := parse(t, fixture)
	root := f.Servers[1]
	var names []string
	for _, d := range root.Directives {
		names = append(names, d.Name)
	}
	if got := strings.Join(names, " "); got != "forward cache" {
		t.Errorf("plugins = %q, want %q", got, "forward cache")
	}

	fwd, _ := root.Directive("forward")
	if len(fwd.Blocks) != 1 || fwd.Blocks[0].Name != "max_concurrent" {
		t.Errorf("nested = %+v", fwd.Blocks)
	}
	// And its raw source survives, which is what a rewrite gives back for
	// everything it did not model.
	if len(fwd.Body) != 1 || !strings.Contains(fwd.Body[0], "max_concurrent 1000") {
		t.Errorf("body = %q, want the source line verbatim", fwd.Body)
	}
}

// The whole point of keeping the source: a rewrite has to give back what it did
// not understand, and it can only do that if it still has it.
func TestABlocksSourceIsKeptVerbatim(t *testing.T) {
	f := parse(t, fixture)
	body := strings.Join(f.Servers[0].Body, "\n")
	for _, want := range []string{"    import logging", "# trailing comment"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q:\n%s", want, body)
		}
	}
}

func TestParseAddress(t *testing.T) {
	for label, want := range map[string]Address{
		"example.com":            {Zone: "example.com.", Port: "53", Scheme: "dns"},
		"example.com.":           {Zone: "example.com.", Port: "53", Scheme: "dns"},
		"EXAMPLE.com:1053":       {Zone: "example.com.", Port: "1053", Scheme: "dns"},
		".":                      {Zone: ".", Port: "53", Scheme: "dns"},
		".:53":                   {Zone: ".", Port: "53", Scheme: "dns"},
		"dns://example.com:5300": {Zone: "example.com.", Port: "5300", Scheme: "dns"},
		"tls://example.com":      {Zone: "example.com.", Port: "853", Scheme: "tls"},
		"grpc://example.com":     {Zone: "example.com.", Port: "443", Scheme: "grpc"},
		"[::1]:5353":             {Zone: "::1.", Port: "5353", Scheme: "dns"},
		// A CIDR on an octet boundary is the reverse zone CoreDNS derives from
		// it; one that is not stays as written, because the answer would be
		// more than one zone and a single wrong one is worse than the source.
		"10.0.0.0/24": {Zone: "0.0.10.in-addr.arpa.", Port: "53", Scheme: "dns"},
		"10.0.0.0/8":  {Zone: "10.in-addr.arpa.", Port: "53", Scheme: "dns"},
		"10.0.0.0/25": {Zone: "10.0.0.0/25", Port: "53", Scheme: "dns"},
	} {
		got := ParseAddress(label)
		if got.Zone != want.Zone || got.Port != want.Port || got.Scheme != want.Scheme {
			t.Errorf("ParseAddress(%q) = {%s %s %s}, want {%s %s %s}",
				label, got.Scheme, got.Zone, got.Port, want.Scheme, want.Zone, want.Port)
		}
	}
}

// The name has to be legal where these objects are served, which is kubectl and
// k9s. A colon is not, and neither is a leading dot.
func TestServerNameIsLegalAndStable(t *testing.T) {
	for labels, want := range map[string]string{
		".:53":                       "root-53",
		"example.com:53":             "example.com-53",
		"example.com example.org:53": "example.com-53",
		"example.com:5353":           "example.com-5353",
		"tls://example.com":          "example.com-853",
		"_tcp.example.com:53":        "tcp.example.com-53",
	} {
		srv := &Server{}
		for _, l := range strings.Fields(labels) {
			srv.Addresses = append(srv.Addresses, ParseAddress(l))
		}
		if got := ServerName(srv); got != want {
			t.Errorf("ServerName(%q) = %q, want %q", labels, got, want)
		}
	}
}

// `file DBFILE [ZONES...]`: naming no zones means the block's own, which is how
// nearly every Corefile writes it.
func TestFileZonesDefaultToTheBlocks(t *testing.T) {
	f := parse(t, fixture)
	srv := f.Servers[0]

	named, _ := srv.Directive("file")
	if got := srv.FileZones(named); len(got) != 1 || got[0] != "example.com." {
		t.Errorf("zones = %v, want the one the directive names", got)
	}

	bare := Directive{Name: "file", Args: []string{"db.example.com"}}
	got := srv.FileZones(bare)
	if len(got) != 2 || got[0] != "example.com." || got[1] != "example.org." {
		t.Errorf("zones = %v, want the block's own", got)
	}
}

// An import of a file is not followed, and a listing that is quietly short
// reads exactly like a machine that is quietly short.
func TestAnUnfollowedImportIsSaidOutLoud(t *testing.T) {
	f := parse(t, "import /etc/coredns/conf.d/*.conf\n\nexample.test:53 {\n    import nowhere\n}\n")

	if len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0], "conf.d") {
		t.Errorf("file warnings = %v, want the top-level import", f.Warnings)
	}
	if len(f.Servers) != 1 {
		t.Fatalf("servers = %d, want the import not to be one", len(f.Servers))
	}
	if len(f.Servers[0].Warnings) != 1 || !strings.Contains(f.Servers[0].Warnings[0], "nowhere") {
		t.Errorf("block warnings = %v, want the unknown snippet", f.Servers[0].Warnings)
	}
}

// CoreDNS substitutes these, so reading the literal would be reporting a port
// the server is not listening on.
func TestEnvironmentIsSubstituted(t *testing.T) {
	t.Setenv("WHOCTL_TEST_PORT", "5354")
	f := parse(t, "example.com:{$WHOCTL_TEST_PORT} {\n    whoami\n}\n")
	if got := f.Servers[0].Port(); got != "5354" {
		t.Errorf("port = %q, want the substituted one", got)
	}
}

// The `root` directive is what relative paths in the block resolve against, and
// getting it wrong means reading the wrong file or none.
func TestRootDirective(t *testing.T) {
	f := parse(t, "example.com:53 {\n    root /var/lib/coredns\n    file db.example.com\n}\n")
	if got := f.Servers[0].Root(); got != "/var/lib/coredns" {
		t.Errorf("root = %q", got)
	}
}
