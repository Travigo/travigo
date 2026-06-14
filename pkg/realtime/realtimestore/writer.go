package realtimestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
)

var ErrEmptyUpdate = errors.New("realtime journey update has no fields")

func WithUpsert(upsert bool) Option {
	return func(opts *options) {
		opts.upsert = upsert
	}
}

func UpdateGenericFields(ctx context.Context, identifier string, fields bson.M, opts ...Option) (*mongo.UpdateResult, error) {
	return UpdateFields(ctx, identifier, fields, opts...)
}

func UpdateFields(ctx context.Context, identifier string, fields bson.M, opts ...Option) (*mongo.UpdateResult, error) {
	cfg := applyOptions(opts...)

	update, err := setUpdate(fields)
	if err != nil {
		return nil, err
	}
	if err := validateIdentifier(identifier); err != nil {
		return nil, err
	}

	return collectionOrDefault(cfg.collection).UpdateOne(
		ctx,
		FilterByIdentifier(identifier),
		update,
		mongooptions.Update().SetUpsert(cfg.upsert),
	)
}

func UpdateFieldsModel(identifier string, fields bson.M, opts ...Option) (mongo.WriteModel, error) {
	cfg := applyOptions(opts...)

	update, err := setUpdate(fields)
	if err != nil {
		return nil, err
	}
	if err := validateIdentifier(identifier); err != nil {
		return nil, err
	}

	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(FilterByIdentifier(identifier))
	updateModel.SetUpdate(update)
	updateModel.SetUpsert(cfg.upsert)

	return updateModel, nil
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64, modificationDateTime time.Time, opts ...Option) (*mongo.UpdateResult, error) {
	return UpdateFields(ctx, identifier, LocationFields(location, bearing, modificationDateTime), opts...)
}

func UpdateLocationModel(identifier string, location ctdf.Location, bearing float64, modificationDateTime time.Time, opts ...Option) (mongo.WriteModel, error) {
	return UpdateFieldsModel(identifier, LocationFields(location, bearing, modificationDateTime), opts...)
}

func UpdateArrivals(ctx context.Context, identifier string, stopUpdates map[string]*ctdf.RealtimeJourneyStops, modificationDateTime time.Time, opts ...Option) (*mongo.UpdateResult, error) {
	return UpdateFields(ctx, identifier, ArrivalFields(stopUpdates, modificationDateTime), opts...)
}

func UpdateArrivalsModel(identifier string, stopUpdates map[string]*ctdf.RealtimeJourneyStops, modificationDateTime time.Time, opts ...Option) (mongo.WriteModel, error) {
	return UpdateFieldsModel(identifier, ArrivalFields(stopUpdates, modificationDateTime), opts...)
}

func UpdateArrival(ctx context.Context, identifier string, stopRef string, stopUpdate *ctdf.RealtimeJourneyStops, modificationDateTime time.Time, opts ...Option) (*mongo.UpdateResult, error) {
	return UpdateArrivals(ctx, identifier, map[string]*ctdf.RealtimeJourneyStops{stopRef: stopUpdate}, modificationDateTime, opts...)
}

func UpdateArrivalModel(identifier string, stopRef string, stopUpdate *ctdf.RealtimeJourneyStops, modificationDateTime time.Time, opts ...Option) (mongo.WriteModel, error) {
	return UpdateArrivalsModel(identifier, map[string]*ctdf.RealtimeJourneyStops{stopRef: stopUpdate}, modificationDateTime, opts...)
}

func BulkWrite(ctx context.Context, models []mongo.WriteModel, opts ...*mongooptions.BulkWriteOptions) (*mongo.BulkWriteResult, error) {
	if len(models) == 0 {
		return &mongo.BulkWriteResult{}, nil
	}

	return collectionOrDefault(nil).BulkWrite(ctx, models, opts...)
}

func LocationFields(location ctdf.Location, bearing float64, modificationDateTime time.Time) bson.M {
	fields := bson.M{
		"vehiclelocation": location,
		"vehiclebearing":  bearing,
	}

	if !modificationDateTime.IsZero() {
		fields["modificationdatetime"] = modificationDateTime
	}

	return fields
}

func ArrivalFields(stopUpdates map[string]*ctdf.RealtimeJourneyStops, modificationDateTime time.Time) bson.M {
	fields := bson.M{}

	if !modificationDateTime.IsZero() {
		fields["modificationdatetime"] = modificationDateTime
	}

	for stopRef, stopUpdate := range stopUpdates {
		if stopUpdate == nil {
			continue
		}

		resolvedStopRef := stopUpdate.StopRef
		if resolvedStopRef == "" {
			resolvedStopRef = stopRef
		}
		if resolvedStopRef == "" {
			continue
		}

		fields[fmt.Sprintf("stops.%s", resolvedStopRef)] = stopUpdate
	}

	return fields
}

func setUpdate(fields bson.M) (bson.M, error) {
	if len(fields) == 0 {
		return nil, ErrEmptyUpdate
	}

	return bson.M{"$set": fields}, nil
}
