package realtime

import "time"

type RealtimeElasticEvent struct {
	Timestamp time.Time

	Success    bool
	FailReason string

	Operator string
	Service  string
}
