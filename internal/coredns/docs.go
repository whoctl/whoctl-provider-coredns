package coredns

import (
	_ "embed"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/docs"

	"github.com/whoctl/whoctl-provider-coredns/resources/server"
	"github.com/whoctl/whoctl-provider-coredns/resources/zone"
)

// The overview is the only page belonging to the provider as a whole; every
// other page lives with the kind it documents, which is why the tree is
// assembled here: go:embed only reaches inside its own package.
//
//go:embed index.md
var indexPage string

// Docs implements core.DocumentedProvider.
func (p *Provider) Docs() core.ProviderDocs {
	return core.ProviderDocs{
		DisplayName: "CoreDNS",
		Summary:     "A local CoreDNS: the server blocks of its Corefile and the zone files they load, read as they are on disk.",
		Categories:  []string{"DNS"},
		Maturity:    "alpha",
		FS:          docs.Tree(pages()),
		// SourceDir and PagePath compose into the file the generator rewrites,
		// so a provider that sets one sets both: the default page layout
		// already carries "resources/" and would land under it twice.
		PagePath:  pagePath,
		SourceDir: "resources",
	}
}

// pagePath maps a kind's singular to where its page lives under resources/.
// One package per kind, page beside the code.
func pagePath(singular string) string { return singular + "/" + singular + ".md" }

func pages() map[string]string {
	return map[string]string{
		"index.md":         indexPage,
		"server/server.md": server.Page,
		"zone/zone.md":     zone.Page,
	}
}
