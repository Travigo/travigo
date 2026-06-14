package realtimestore

import (
	"errors"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
)

const CollectionName = "realtime_journeys"

var ErrEmptyIdentifier = errors.New("realtime journey identifier is required")

type Option func(*options)

type options struct {
	upsert         bool
	collection     *mongo.Collection
	findOneOptions []*mongooptions.FindOneOptions
	findOptions    []*mongooptions.FindOptions
}

func WithCollection(collection *mongo.Collection) Option {
	return func(opts *options) {
		opts.collection = collection
	}
}

func applyOptions(opts ...Option) options {
	cfg := options{}

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return cfg
}

func collectionOrDefault(collection *mongo.Collection) *mongo.Collection {
	if collection != nil {
		return collection
	}

	return database.GetCollection(CollectionName)
}
