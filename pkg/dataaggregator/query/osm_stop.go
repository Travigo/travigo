package query

import "github.com/travigo/travigo/pkg/ctdf"

const MaximumOSMStopRadiusMetres = 5000

type OSMStop struct {
	Stop *ctdf.Stop

	ForceRefresh bool
	RadiusMetres int
}
