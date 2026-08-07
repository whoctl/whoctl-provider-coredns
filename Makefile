# whoctl-provider-coredns
#
# The unit tests read testdata/ and nothing else, so they are safe on the host
# and stay safe: this provider does not write, and the one thing that could go
# wrong — reading the developer's own /etc/coredns — is what the fixture root
# exists to prevent.

VERSION ?= dev
export VERSION

.DEFAULT_GOAL := help

## build: build the provider binary
.PHONY: build
build:
	@mkdir -p bin
	@CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" \
		-o bin/whoctl-provider-coredns .
	@echo "built bin/whoctl-provider-coredns ($(VERSION))"

## test: unit tests on the host, plus e2e against a real CoreDNS
.PHONY: test
test:
	@echo "== unit tests (host, reads testdata/ only)"
	@go test ./...
	@echo
	@scripts/e2e-run.sh

## unit: unit tests only
.PHONY: unit
unit:
	@go test ./...

## sandbox: a shell on a throwaway machine, with the fixture at /etc/coredns
#
# The harness is whoctl's — see scripts/sandbox.sh on what is prepared here and
# why. It needs github.com/whoctl/whoctl checked out beside this repository, or
# WHOCTL_SANDBOX pointing at its scripts/sandbox.sh.
.PHONY: sandbox
sandbox:
	@scripts/sandbox.sh $(ARGS)

## e2e: the suite, against a CoreDNS reading the same files whoctl does
.PHONY: e2e
e2e:
	@scripts/e2e-run.sh

## coredns: start a CoreDNS on the fixture and leave it up to dig at by hand
.PHONY: coredns
coredns:
	@scripts/coredns.sh

## coredns-stop: take that CoreDNS down
.PHONY: coredns-stop
coredns-stop:
	@scripts/coredns.sh stop

## validate: have CoreDNS confirm the fixture Corefile is one, without whoctl
#
# `make e2e` subsumes this — a CoreDNS that would not load the fixture never
# answers a single query there. It is kept because it needs no whoctl at all,
# so it still says something when the two are being changed together.
.PHONY: validate
validate:
	@scripts/validate.sh

## docs: write the documentation bundle a release publishes
.PHONY: docs
docs:
	@go run . --docs-bundle > bundle.json
	@echo "wrote bundle.json"

## docs-generate: refresh the generated tables in each kind's page
.PHONY: docs-generate
docs-generate:
	@go run . --docs-generate

## fmt: format and vet
.PHONY: fmt
fmt:
	@gofmt -w .
	@go vet ./...

## standalone: build and test without the workspace, the way a consumer does
#
# The check lives in whoctl, beside the container harness and for the same
# reason: it is about how a module is consumed, not about what this one manages.
.PHONY: standalone
standalone:
	@../whoctl/scripts/standalone.sh

## help: list the targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
