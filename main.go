// Command whoctl-provider-coredns serves the coredns provider over whoctl's
// provider protocol, on stdin and stdout.
//
// This is what a provider binary is, and it is deliberately this short: the
// provider itself knows nothing about the protocol, and the protocol knows
// nothing about CoreDNS.
package main

import (
	"fmt"
	"os"

	"github.com/whoctl/whoctl-sdk-go/core"
	"github.com/whoctl/whoctl-sdk-go/docs"
	"github.com/whoctl/whoctl-sdk-go/protocol"
	"github.com/whoctl/whoctl-sdk-go/sysexec"

	"github.com/whoctl/whoctl-provider-coredns/internal/coredns"
)

// version is stamped at build time with -ldflags.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		if err := docsCommand(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "whoctl-provider-coredns:", err)
			os.Exit(1)
		}
		return
	}

	err := protocol.ServeProcess(func(cfg protocol.Config) (core.Provider, error) {
		// The runner is built from the session's configuration, which is what
		// makes --dry-run and -v mean something on this side of the pipe. Its
		// output goes to stderr, which whoctl passes straight through.
		runner := &sysexec.Runner{DryRun: cfg.DryRun, Verbose: cfg.Verbose, Out: os.Stderr}
		// There is no flag for the Corefile, only --root and the environment.
		// A flag would have to reach this process through whoctl, which would
		// mean whoctl knowing what a Corefile is — and that is the abstraction
		// leaking. See internal/provider on where the path comes from.
		//
		// Nothing is read here either: a machine with no CoreDNS on it still
		// has to answer `whoctl resources` and `whoctl docs`.
		return coredns.New(coredns.Options{Root: cfg.Root, Runner: runner}), nil
	}, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "whoctl-provider-coredns:", err)
		os.Exit(1)
	}
}

// docsCommand handles the two documentation flags.
//
// A release publishes this provider's documentation alongside its binary, and
// the binary is what has it: the pages are embedded here and the field tables
// come from this package's own structs.
//
// --docs-generate rewrites the generated regions of the pages in the checkout.
// TestConformance fails when they are stale and names a command; this is that
// command, and it lives here because a provider must not need a site builder to
// keep its own documentation current.
func docsCommand(arg string) error {
	p := coredns.New(coredns.Options{})
	switch arg {
	case "--docs-bundle":
		return docs.WriteBundle(os.Stdout, p, version)
	case "--docs-generate":
		site, err := docs.Build([]core.Provider{p}, docs.Options{})
		if err != nil {
			return err
		}
		written, err := docs.Generate(site, ".")
		if err != nil {
			return err
		}
		for _, file := range written {
			fmt.Fprintln(os.Stderr, "wrote", file)
		}
	default:
		return fmt.Errorf("unknown flag %q: this is a provider binary, and whoctl is what runs it", arg)
	}
	return nil
}
