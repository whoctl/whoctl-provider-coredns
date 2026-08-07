package main

import (
	"testing"

	"github.com/whoctl/whoctl-sdk-go/providertest"

	"github.com/whoctl/whoctl-provider-coredns/internal/coredns"
)

// The whole of this provider's contract with whoctl, in one test. It reads the
// resource types and the embedded pages; it opens no Corefile, so it works the
// same on a machine that has CoreDNS and on one that does not.
func TestConformance(t *testing.T) {
	providertest.Conformance(t, coredns.New(coredns.Options{}), providertest.Options{SourceRoot: "."})
}
