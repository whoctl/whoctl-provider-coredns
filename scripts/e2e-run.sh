#!/bin/sh
# Runs this provider's end-to-end suite against a CoreDNS reading the same files.
#
# The machine it runs on, and the CoreDNS beside it, are scripts/sandbox.sh.
# Everything about how those are built lives there; this is the suite and
# nothing else.
#
# Two ways of preparing the same container would be two things to drift, and
# the shell somebody opens by hand has to be the same one the assertions run
# against — otherwise reproducing a failure means reproducing the difference
# first, and the difference is the part nobody knows about.
#
# Usage:
#   scripts/e2e-run.sh
set -eu

exec "$(cd "$(dirname "$0")" && pwd)/sandbox.sh" /scripts/e2e.sh
