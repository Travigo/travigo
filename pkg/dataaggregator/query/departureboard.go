package query

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
)

type DepartureBoard struct {
	Stop          *ctdf.Stop
	Count         int
	StartDateTime time.Time
	Filter        *bson.M
}
