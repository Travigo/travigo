package realtime

import "time"

type RealtimeIdentifyFailureElasticEvent struct {
	Timestamp time.Time

	FailReason string

	Operator string
	Service  string
}
