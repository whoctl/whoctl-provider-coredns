package provider

import (
	"context"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// ReadOnly implements the mutating half of core.Handler for a kind that does
// not have one yet, so each kind states its name once and shares the rest.
//
// A Corefile can be written, and one day this provider will. The reason it does
// not yet is the one written at the top of internal/corefile: a rewrite has to
// give back every plugin, argument and comment it did not model, and until that
// is proven by tests, writing would mean handing somebody a DNS server missing
// the half of its configuration whoctl did not understand.
//
// The kinds publish `get, list` and nothing else, so a Kubernetes client greys
// the edit out instead of offering it and failing.
type ReadOnly struct{ Kind string }

func (r ReadOnly) Apply(context.Context, core.Object) (core.Result, error) {
	return core.Result{}, r.refuse("changed")
}

func (r ReadOnly) Delete(context.Context, string) error { return r.refuse("deleted") }

func (r ReadOnly) refuse(verb string) error {
	return core.Unsupportedf("%s cannot be %s yet: this provider reads a Corefile and does not rewrite one", r.Kind, verb)
}
