package realtime

import (
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
)

type RealtimeIdentifyFailureElasticEvent struct {
	Timestamp time.Time

	Success    bool
	FailReason string

	Operator string
	Service  string
}

type BusLocationElasticEvent struct {
	Timestamp time.Time

	Location ctdf.ElasticGeoPoint
}
