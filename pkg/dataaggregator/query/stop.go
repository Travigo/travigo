package query

import "go.mongodb.org/mongo-driver/bson"

type Stop struct {
	Identifier string
}

func (s *Stop) ToBson() bson.M {
	if s.Identifier != "" {
		return bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": s.Identifier},
			bson.M{"otheridentifiers": s.Identifier},
		}}
	}

	return nil
}
