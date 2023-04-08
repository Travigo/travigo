package query

import "go.mongodb.org/mongo-driver/bson"

type StopGroup struct {
	Identifier string
}

func (s *StopGroup) ToBson() bson.M {
	if s.Identifier != "" {
		return bson.M{"identifier": s.Identifier}
	}

	return nil
}
