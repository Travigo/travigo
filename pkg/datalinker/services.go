package datalinker

import (
	"context"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func enrichStagedServices(ctx context.Context, collection *mongo.Collection) error {
	rules := datasets.NewServiceOverrides(datasets.LoadDataSources())
	// Walk the immutable _id index while updating the staged service fields.
	cursor, err := collection.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return fmt.Errorf("read services for enrichment: %w", err)
	}
	defer cursor.Close(ctx)

	const batchSize = 500
	updates := make([]mongo.WriteModel, 0, batchSize)
	flush := func() error {
		if len(updates) == 0 {
			return nil
		}
		if _, err := collection.BulkWrite(ctx, updates); err != nil {
			return fmt.Errorf("write enriched services: %w", err)
		}
		updates = updates[:0]
		return nil
	}
	for cursor.Next(ctx) {
		var staged struct {
			ID           any `bson:"_id"`
			ctdf.Service `bson:",inline"`
		}
		if err := cursor.Decode(&staged); err != nil {
			return fmt.Errorf("decode service for enrichment: %w", err)
		}
		service := &staged.Service
		if !rules.Apply(service) {
			continue
		}
		fields := bson.M{
			"servicename":          service.ServiceName,
			"description":          service.Description,
			"website":              service.Website,
			"routes":               service.Routes,
			"brandcolour":          service.BrandColour,
			"secondarybrandcolour": service.SecondaryBrandColour,
			"brandicon":            service.BrandIcon,
			"branddisplaymode":     service.BrandDisplayMode,
		}
		if service.PublisherServiceName != nil {
			fields["publisherservicename"] = *service.PublisherServiceName
		}
		updates = append(updates, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: staged.ID}}).
			SetUpdate(bson.M{"$set": fields}))
		if len(updates) == batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("scan services for enrichment: %w", err)
	}
	return flush()
}
