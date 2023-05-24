package ctdf

import (
	"time"
)

type QueryDepartureBoard struct {
	Stop          *Stop
	Count         int
	StartDateTime time.Time
}
