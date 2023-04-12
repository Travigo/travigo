package query

import (
	"github.com/britbus/britbus/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
)

type Service struct {
	PrimaryIdentifier string
}

func (s *Service) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}

type ServicesByStop struct {
	Stop *ctdf.Stop
}
