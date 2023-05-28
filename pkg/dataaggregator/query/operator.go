package query

import "go.mongodb.org/mongo-driver/bson"

type Operator struct {
	PrimaryIdentifier string
	AnyIdentifier     string
}

func (o *Operator) ToBson() bson.M {
	if o.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": o.PrimaryIdentifier}
	} else if o.AnyIdentifier != "" {
		return bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": o.AnyIdentifier},
			bson.M{"otheridentifiers": o.AnyIdentifier},
		}}
	}

	return nil
}
