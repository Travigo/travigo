package realtimestore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

func FilterByIdentifier(identifier string) bson.M {
	return bson.M{"primaryidentifier": identifier}
}

func FilterByOtherIdentifier(name string, value string) bson.M {
	return bson.M{fmt.Sprintf("otheridentifiers.%s", name): value}
}

func validateIdentifier(identifier string) error {
	if identifier == "" {
		return ErrEmptyIdentifier
	}

	return nil
}
