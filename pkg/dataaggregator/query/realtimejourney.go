package query

import "go.mongodb.org/mongo-driver/bson"

type RealtimeJourney struct {
	PrimaryIdentifier string
}

func (r *RealtimeJourney) ToBson() bson.M {
	if r.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": r.PrimaryIdentifier}
	}

	return nil
}
