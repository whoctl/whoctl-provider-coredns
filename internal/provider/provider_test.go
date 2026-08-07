package provider

import "testing"

// The Corefile is found by option, then environment, then the packaged
// location. A machine with no CoreDNS still gets an answer with a path in it.
func TestCorefileIsFoundInOrder(t *testing.T) {
	if got := New(Options{Corefile: "/tmp/a/Corefile"}).Corefile(); got != "/tmp/a/Corefile" {
		t.Errorf("option = %q", got)
	}
	t.Setenv(CorefileEnv, "/tmp/b/Corefile")
	if got := New(Options{}).Corefile(); got != "/tmp/b/Corefile" {
		t.Errorf("environment = %q", got)
	}
	if got := New(Options{Corefile: "/tmp/a/Corefile"}).Corefile(); got != "/tmp/a/Corefile" {
		t.Errorf("the option lost to the environment: %q", got)
	}
	t.Setenv(CorefileEnv, "")
	if got := New(Options{}).Corefile(); got != "/"+DefaultCorefile {
		t.Errorf("default = %q, want the packaged location under /", got)
	}
	if got := New(Options{Root: "/mnt/image"}).Corefile(); got != "/mnt/image/"+DefaultCorefile {
		t.Errorf("rooted default = %q", got)
	}
}

// A server context can only set an environment, and it has three CoreDNS trees
// to point at where whoctl's own --root is one value for the session.
//
// # Why this test exists
//
// Without it, a context could name a Corefile anywhere and every absolute path
// inside it — `root /etc/coredns/zones`, which is ordinary — still resolved
// against the server's own filesystem. The unit tests passed because they set
// Root directly; the server, which cannot, listed one zone as missing and read
// the host's own /etc for the rest.
func TestTheRootComesFromTheEnvironmentWhenNothingElseSaid(t *testing.T) {
	t.Setenv(RootEnv, "/mnt/image")
	if got := New(Options{}).Root; got != "/mnt/image" {
		t.Errorf("root = %q, want the environment's", got)
	}
	// --root is what somebody typed for this command, so it wins.
	if got := New(Options{Root: "/other"}).Root; got != "/other" {
		t.Errorf("root = %q, want the option to win", got)
	}
}

// Resolve is where a path written inside the Corefile becomes a path to open,
// and getting it wrong means reading a file that belongs to somebody else.
func TestResolve(t *testing.T) {
	p := New(Options{Root: "/mnt/image", Corefile: "/mnt/image/etc/coredns/Corefile"})

	for _, tc := range []struct{ path, root, want string }{
		// Relative with no `root` directive: beside the Corefile, which is
		// where every packaged layout puts it.
		{"db.example.com", "", "/mnt/image/etc/coredns/db.example.com"},
		// Relative under a `root` directive, which is itself absolute.
		{"db.example.com", "/etc/coredns/zones", "/mnt/image/etc/coredns/zones/db.example.com"},
		// Already absolute: still under the tree being described.
		{"/var/lib/coredns/db.example.com", "", "/mnt/image/var/lib/coredns/db.example.com"},
		{"", "", ""},
	} {
		if got := p.Resolve(tc.path, tc.root); got != tc.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tc.path, tc.root, got, tc.want)
		}
	}

	// An empty root means the machine itself, and nothing is rewritten.
	live := New(Options{})
	if got := live.Resolve("/etc/coredns/db.example.com", ""); got != "/etc/coredns/db.example.com" {
		t.Errorf("Resolve on the live machine = %q", got)
	}
}
