// Package railclassdata embeds the rail class catalogue used by the importers.
package railclassdata

import "embed"

//go:embed *.yaml
var Files embed.FS
