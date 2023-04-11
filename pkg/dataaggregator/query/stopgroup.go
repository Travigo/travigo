package query

import "go.mongodb.org/mongo-driver/bson"

type StopGroup struct {
	PrimaryIdentifier string
}

func (s *StopGroup) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}
