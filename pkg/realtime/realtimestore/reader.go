package realtimestore

import (
	"context"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
)

func WithFindOneOptions(findOptions ...*mongooptions.FindOneOptions) Option {
	return func(opts *options) {
		opts.findOneOptions = append(opts.findOneOptions, findOptions...)
	}
}

func WithFindOptions(findOptions ...*mongooptions.FindOptions) Option {
	return func(opts *options) {
		opts.findOptions = append(opts.findOptions, findOptions...)
	}
}

func GetByIdentifier(ctx context.Context, identifier string, opts ...Option) (*ctdf.RealtimeJourney, error) {
	if err := validateIdentifier(identifier); err != nil {
		return nil, err
	}

	return FindOne(ctx, FilterByIdentifier(identifier), opts...)
}

func FindOne(ctx context.Context, filter interface{}, opts ...Option) (*ctdf.RealtimeJourney, error) {
	cfg := applyOptions(opts...)

	var realtimeJourney *ctdf.RealtimeJourney
	err := collectionOrDefault(cfg.collection).FindOne(ctx, filter, cfg.findOneOptions...).Decode(&realtimeJourney)
	if err != nil {
		return nil, err
	}

	locationDescription, err := GetLocationDescription(ctx, realtimeJourney.PrimaryIdentifier)
	if err != nil {
		realtimeJourney.VehicleLocationDescription = locationDescription
	}

	return realtimeJourney, nil
}

func Find(ctx context.Context, filter interface{}, opts ...Option) ([]*ctdf.RealtimeJourney, error) {
	cfg := applyOptions(opts...)

	cursor, err := collectionOrDefault(cfg.collection).Find(ctx, filter, cfg.findOptions...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var realtimeJourneys []*ctdf.RealtimeJourney
	if err := cursor.All(ctx, &realtimeJourneys); err != nil {
		return nil, err
	}

	return realtimeJourneys, nil
}

func GetLocationDescription(ctx context.Context, identifier string) (string, error) {
	description := redis_client.Client.Get(ctx, fmt.Sprintf("realtime-journey:%s/locationdescription", identifier))
	if description.Err() != nil {
		return "", description.Err()
	}
	return description.Val(), nil
}
