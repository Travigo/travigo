package ctdf

import "go.mongodb.org/mongo-driver/bson"

type QueryOperatorGroup struct {
	Identifier string
}

func (o *QueryOperatorGroup) ToBson() bson.M {
	if o.Identifier != "" {
		return bson.M{"identifier": o.Identifier}
	}

	return nil
}
