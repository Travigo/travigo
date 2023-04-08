package query

import "go.mongodb.org/mongo-driver/bson"

type OperatorGroup struct {
	Identifier string
}

func (o *OperatorGroup) ToBson() bson.M {
	if o.Identifier != "" {
		return bson.M{"identifier": o.Identifier}
	}

	return nil
}
