package query

import (
	"go.mongodb.org/mongo-driver/bson"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type DepartureBoard struct {
	Stop          *ctdf.Stop
	Count         int
	StartDateTime time.Time
	Filter        *bson.M
}
