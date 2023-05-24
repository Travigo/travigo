package ctdf

import (
	"go.mongodb.org/mongo-driver/bson"
)

type QueryService struct {
	PrimaryIdentifier string
}

func (s *QueryService) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}

type QueryServicesByStop struct {
	Stop *Stop
}
