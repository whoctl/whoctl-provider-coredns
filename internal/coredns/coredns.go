// Package coredns assembles the provider from its resources.
//
// It is a separate package from internal/provider because the two point at each
// other otherwise: every resource needs the shared state, and the list of
// resources needs every resource. Assembly is the third place that imports
// both, and it is the only file that changes when a kind is added.
package coredns

import (
	"github.com/whoctl/whoctl-sdk-go/core"

	"github.com/whoctl/whoctl-provider-coredns/internal/provider"
	"github.com/whoctl/whoctl-provider-coredns/resources/record"
	"github.com/whoctl/whoctl-provider-coredns/resources/server"
	"github.com/whoctl/whoctl-provider-coredns/resources/zone"
)

// Provider is the coredns provider.
type Provider struct{ *provider.Provider }

// Options configures it.
type Options = provider.Options

// New builds the provider with every kind it serves.
func New(opts Options) *Provider {
	return &Provider{Provider: provider.New(opts)}
}

// Handlers implements core.Provider.
func (p *Provider) Handlers() []core.Handler {
	return []core.Handler{
		server.New(p.Provider),
		zone.New(p.Provider),
		record.New(p.Provider),
	}
}

var _ core.Provider = (*Provider)(nil)
