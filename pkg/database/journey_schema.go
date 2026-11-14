package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Require a calls array and reject geometry and the old leg array even for
// server-side writes, which bypass the CTDF storage encoder.
var journeyValidator = bson.D{
	{Key: "calls", Value: bson.D{{Key: "$type", Value: "array"}}},
	{Key: "path", Value: bson.D{{Key: "$exists", Value: false}}},
	{Key: "track", Value: bson.D{{Key: "$exists", Value: false}}},
	{Key: "calls.outgoing.track", Value: bson.D{{Key: "$exists", Value: false}}},
}

func EnsureJourneyCollection(ctx context.Context, name string) error {
	db := Instance.Database
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return fmt.Errorf("inspect %s journey collection: %w", name, err)
	}
	if len(specs) == 0 {
		err := db.CreateCollection(ctx, name, options.CreateCollection().
			SetValidator(journeyValidator).SetValidationLevel("strict").SetValidationAction("error"))
		if err == nil {
			return nil
		}
		var serverError mongo.ServerError
		if !errors.As(err, &serverError) || !serverError.HasErrorCode(48) { // NamespaceExists
			return fmt.Errorf("create %s journey collection: %w", name, err)
		}
		// Another importer may have created the collection concurrently.
		specs, err = db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
		if err != nil {
			return fmt.Errorf("inspect newly created %s journey collection: %w", name, err)
		}
	}
	if len(specs) == 1 {
		spec := specs[0].Options
		validator, _ := spec.Lookup("validator").DocumentOK()
		level, _ := spec.Lookup("validationLevel").StringValueOK()
		action, _ := spec.Lookup("validationAction").StringValueOK()
		expected, err := bson.Marshal(journeyValidator)
		if err != nil {
			return err
		}
		if bytes.Equal(validator, expected) && (level == "strict" || level == "") && (action == "error" || action == "") {
			return nil
		}
	}
	err = db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: name},
		{Key: "validator", Value: journeyValidator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}).Err()
	if err != nil {
		return fmt.Errorf("set %s journey collection validation: %w", name, err)
	}
	return nil
}
