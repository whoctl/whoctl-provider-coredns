package record

import _ "embed"

// Page is this kind's documentation, embedded so it travels with the binary and
// reaches the site as part of the provider's bundle.
//
//go:embed record.md
var Page string
