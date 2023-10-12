package query

import (
	"go.mongodb.org/mongo-driver/bson"
)

type ServiceAlertsForMatchingIdentifiers struct {
	MatchingIdentifiers []string
}

func (s *ServiceAlertsForMatchingIdentifiers) ToBson() bson.M {
	return bson.M{"matchedidentifiers": bson.M{"$in": s.MatchingIdentifiers}}
}
