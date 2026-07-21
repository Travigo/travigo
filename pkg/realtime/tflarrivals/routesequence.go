package tflarrivals

import "github.com/travigo/travigo/pkg/ctdf"

type OrderedLineRoute struct {
	Name      string
	NaptanIDs []string

	Direction    string
	LegTracks    [][]ctdf.Location
	LegTrackRefs []string
}
