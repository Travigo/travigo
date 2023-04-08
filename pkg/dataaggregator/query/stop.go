package query

import "go.mongodb.org/mongo-driver/bson"

type Stop struct {
	PrimaryIdentifier string
}

func (s *Stop) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}
