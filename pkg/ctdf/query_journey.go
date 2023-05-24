package ctdf

import "go.mongodb.org/mongo-driver/bson"

type QueryJourney struct {
	PrimaryIdentifier string
}

func (j *QueryJourney) ToBson() bson.M {
	if j.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": j.PrimaryIdentifier}
	}

	return nil
}
