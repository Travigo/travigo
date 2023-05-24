package ctdf

import "go.mongodb.org/mongo-driver/bson"

type QueryStopGroup struct {
	PrimaryIdentifier string
}

func (s *QueryStopGroup) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}
