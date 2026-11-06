package railtd

import "github.com/travigo/travigo/pkg/ctdf"

const SMARTCollection = "gb_networkrail_td_smart"
const SMARTSnapshotID = "current"

type SMARTStep struct {
	TD, FROMBERTH, TOBERTH, STANOX, STEPTYPE, EVENT string
	BERTHOFFSET                                     interface{} `bson:"berthoffset,omitempty"`
}

type SMARTLocation struct {
	Refs  []string
	Point ctdf.Location
}

type SMARTSnapshot struct {
	Steps     []SMARTStep
	Locations map[string]SMARTLocation
}
