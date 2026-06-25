package query

import "github.com/travigo/travigo/pkg/ctdf"

type OSMStop struct {
	Stop *ctdf.Stop

	ForceRefresh bool
	RadiusMetres int
}
