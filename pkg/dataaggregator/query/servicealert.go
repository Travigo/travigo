package query

import (
	"go.mongodb.org/mongo-driver/bson"
)

type ServiceAlertsForMatchingIdentifier struct {
	MatchingIdentifier string
}

func (s *ServiceAlertsForMatchingIdentifier) ToBson() bson.M {
	if s.MatchingIdentifier != "" {
		return bson.M{"matchedidentifiers": s.MatchingIdentifier}
	}

	return nil
}
