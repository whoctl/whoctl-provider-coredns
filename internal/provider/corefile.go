package provider

import (
	"errors"
	"io/fs"
	"os"

	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/corefile"
)

// LoadCorefile reads and parses the configuration.
//
// Both kinds start here, which is why the error is written once: a machine with
// no CoreDNS on it is the ordinary case for this provider — somebody running
// `whoctl get coredns/servers` on their laptop — and the answer has to say
// which path was looked at and how to change it, not just that something was
// missing.
func (p *Provider) LoadCorefile() (*corefile.File, error) {
	path := p.Corefile()
	f, err := corefile.ParseFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, core.Unavailablef("no Corefile at %s: point %s at one, or pass --root if CoreDNS is under a different tree", path, CorefileEnv)
	case errors.Is(err, fs.ErrPermission):
		return nil, core.Unavailablef("cannot read %s: a packaged CoreDNS keeps it readable only by root", path)
	case err != nil:
		return nil, core.Internalf("reading %s: %w", path, err)
	}
	return f, nil
}

// FileTime is when a file was last written, which is the only timestamp a
// configuration file carries.
//
// It becomes metadata.creationTimestamp, and that is a small stretch made
// deliberately: a Corefile records no creation time, an AGE column reading
// "this configuration last changed 3d ago" is the useful answer, and the
// alternative — a blank column — tells nobody anything.
func FileTime(path string) core.Time {
	info, err := os.Stat(path)
	if err != nil {
		return core.Time{}
	}
	return core.NewTime(info.ModTime())
}
