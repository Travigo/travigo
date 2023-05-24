package ctdf

import (
	"go.mongodb.org/mongo-driver/bson"
)

type QueryRealtimeJourney struct {
	PrimaryIdentifier string
}

func (r *QueryRealtimeJourney) ToBson() bson.M {
	if r.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": r.PrimaryIdentifier}
	}

	return nil
}

type RealtimeJourneyForJourney struct {
	Journey *Journey
}
