package query

import "go.mongodb.org/mongo-driver/bson"

type Journey struct {
	PrimaryIdentifier string
}

func (j *Journey) ToBson() bson.M {
	if j.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": j.PrimaryIdentifier}
	}

	return nil
}
