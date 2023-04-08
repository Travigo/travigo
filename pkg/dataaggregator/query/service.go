package query

import "go.mongodb.org/mongo-driver/bson"

type Service struct {
	PrimaryIdentifier string
}

func (s *Service) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}
