package query

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type DepartureBoard struct {
	Stop          *ctdf.Stop
	Count         int
	StartDateTime time.Time
}
