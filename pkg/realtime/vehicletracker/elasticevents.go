package vehicletracker

import (
	"time"
)

type RealtimeIdentifyFailureElasticEvent struct {
	Timestamp time.Time

	Success    bool
	FailReason string

	Operator string
	Service  string
}
