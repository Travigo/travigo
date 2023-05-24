package ctdf

import "go.mongodb.org/mongo-driver/bson"

type QueryStop struct {
	PrimaryIdentifier string
}

func (s *QueryStop) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}
