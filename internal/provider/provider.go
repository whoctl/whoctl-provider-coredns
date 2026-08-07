// Package provider is the state every coredns resource works from: where the
// Corefile is, what the paths inside it are relative to, and the runner.
//
// It exists because each kind is its own package under resources/, and they all
// need the same handful of things.
package provider

import (
	"os"
	"path/filepath"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/sysexec"
)

// API group and version served by this provider.
//
// The group is flat — coredns.whoctl.io, not <something>.coredns.whoctl.io —
// because CoreDNS is one thing and not a catalogue of services. The aws
// provider needs the extra label because Instance exists under ec2 and under
// rds and one must not answer for the other; nothing here collides, so
// `coredns/servers` is two segments and stays that way.
const (
	Group   = "coredns.whoctl.io"
	Version = "v1alpha1"
)

// DefaultCorefile is where a packaged CoreDNS keeps its configuration, relative
// to the configured root. CoreDNS's own default is "Corefile" in the working
// directory, which is right for running it by hand and wrong for describing a
// machine — every distribution package and the upstream systemd unit point at
// this path instead.
const DefaultCorefile = "etc/coredns/Corefile"

// CorefileEnv names the Corefile and RootEnv the tree its paths resolve under,
// for a caller that cannot pass an option.
//
// This is the same arrangement the aws provider has with AWS_PROFILE and
// friends: run by hand it reads what is in the environment, run by a whoctl
// server it reads whatever that server put in the environment for one of its
// contexts, and the provider never learns which of the two started it. CoreDNS
// has no environment variable of its own for either — it takes -conf and a
// working directory — so the names are whoctl's.
//
// RootEnv exists because whoctl's own --root is one value for the whole
// session, and a server serving three CoreDNS trees needs three. Without it a
// context can point at a Corefile and still have every absolute path inside it
// — `root /etc/coredns/zones`, which is ordinary — resolve against the server's
// own filesystem, which is a listing of files that belong to somebody else.
const (
	CorefileEnv = "WHOCTL_COREDNS_COREFILE"
	RootEnv     = "WHOCTL_COREDNS_ROOT"
)

// Options configures the provider.
type Options struct {
	// Root is the filesystem root reads are relative to. Empty means "/". It
	// exists so tests can point at a fixture tree instead of the real machine,
	// and so a context can describe a mounted image.
	Root string

	// Corefile overrides where the configuration is read from. It wins over
	// the environment and over Root's default location, and it is what a test
	// sets.
	Corefile string

	// Runner runs the native tools. Required for any mutating verb, of which
	// there are none yet.
	Runner *sysexec.Runner
}

// Provider is what every kind here works from.
type Provider struct {
	// Root is what a path inside the Corefile is resolved under. Empty means
	// "/".
	Root string

	// Runner runs the native tools, and is what --dry-run and -v act on.
	Runner *sysexec.Runner

	corefile string
}

// New builds the coredns provider.
func New(opts Options) *Provider {
	runner := opts.Runner
	if runner == nil {
		runner = &sysexec.Runner{}
	}
	root := opts.Root
	if root == "" {
		// whoctl's --root wins, because somebody typed it for this command.
		root = os.Getenv(RootEnv)
	}
	return &Provider{Root: root, Runner: runner, corefile: opts.Corefile}
}

// Name implements core.Provider. It is the prefix every kind here is addressed
// by: `whoctl get coredns/servers`.
func (p *Provider) Name() string { return "coredns" }

// Corefile is the path the configuration is read from.
//
// Nothing is read here and no error is returned: a machine with no CoreDNS at
// all still has to answer `whoctl resources` and `whoctl docs`, and the missing
// file is reported by whoever opens it, once, with the path in the message.
func (p *Provider) Corefile() string {
	if p.corefile != "" {
		return p.corefile
	}
	if env := os.Getenv(CorefileEnv); env != "" {
		return env
	}
	// The leading slash is what makes an empty Root mean "/" rather than a
	// path relative to the working directory.
	return filepath.Join(p.Root, "/"+DefaultCorefile)
}

// Resolve turns a path written inside the Corefile into a path to open.
//
// CoreDNS resolves a relative path against its working directory, or against
// the `root` directive when the server block has one. Absolute paths go under
// Root, which is what makes a fixture tree and a mounted image behave the same
// as the live machine.
func (p *Provider) Resolve(path, root string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if root != "" {
			path = filepath.Join(root, path)
		} else {
			// Relative to the Corefile is not what CoreDNS does — it is
			// relative to the working directory, which is whatever systemd or
			// a shell happened to set. Beside the Corefile is where the files
			// actually are in every packaged layout, and it is the only answer
			// that does not depend on how the daemon was started.
			return filepath.Join(filepath.Dir(p.Corefile()), path)
		}
	}
	if p.Root == "" {
		return path
	}
	return filepath.Join(p.Root, path)
}

// ResourceType fills in the group and version shared by every kind here.
func ResourceType(t core.ResourceType) core.ResourceType {
	t.Group = Group
	t.Version = Version
	return t
}
