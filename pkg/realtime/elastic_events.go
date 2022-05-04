package realtime

import (
	"time"
)

type ElasticGeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type RealtimeIdentifyFailureElasticEvent struct {
	Timestamp time.Time

	Success    bool
	FailReason string

	Operator string
	Service  string
}

type BusLocationElasticEvent struct {
	Timestamp time.Time

	Location ElasticGeoPoint
}
