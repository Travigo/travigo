package query

import (
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
)

type TimetableRecords struct {
	Stop          *ctdf.Stop
	Count         int
	StartDateTime time.Time
}
