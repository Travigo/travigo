package journeytracks

import (
	"context"
	"fmt"
	"slices"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// RemoveDatasets clears references on retained journeys before deleting the
// retired datasets' track geometry and route metadata.
func RemoveDatasets(ctx context.Context, datasetIDs []string) error {
	filter := bson.M{"datasource.datasetid": bson.M{"$in": datasetIDs}}
	owned, err := loadTrackRefs(ctx, filter)
	if err != nil {
		return err
	}
	refs := make([]string, 0, len(owned))
	for ref := range owned {
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysCollectionName} {
		collection := database.GetCollection(name)
		for batch := range slices.Chunk(refs, 5000) {
			trackRefs := bson.M{"$in": batch}
			if _, err := collection.UpdateMany(ctx, bson.M{"trackref": trackRefs},
				bson.M{"$unset": bson.M{"trackref": "", "track": ""}}); err != nil {
				return fmt.Errorf("clear journey track references in %s: %w", name, err)
			}
			if _, err := collection.UpdateMany(ctx, bson.M{"path.trackref": trackRefs},
				bson.M{"$unset": bson.M{"path.$[leg].trackref": "", "path.$[leg].track": ""}},
				options.Update().SetArrayFilters(options.ArrayFilters{Filters: []interface{}{
					bson.M{"leg.trackref": trackRefs},
				}})); err != nil {
				return fmt.Errorf("clear path track references in %s: %w", name, err)
			}
		}
	}
	for _, name := range []string{"journey_track_routes", "journey_tracks"} {
		if _, err := database.GetCollection(name).DeleteMany(ctx, filter); err != nil {
			return fmt.Errorf("delete data from %s: %w", name, err)
		}
	}
	return nil
}

// CleanupOSMRailTracks keeps geometry still referenced by a timetable generation.
func CleanupOSMRailTracks(ctx context.Context, source *ctdf.DataSourceReference) error {
	filter := bson.M{
		"datasource.datasetid":      source.DatasetID,
		"datasource.originalformat": source.OriginalFormat,
		"datasource.timestamp":      bson.M{"$lt": source.Timestamp},
	}
	stale, err := loadTrackRefs(ctx, filter)
	if err != nil {
		return err
	}

	// Check the collections in publication order so a journey copied by the
	// linker during cleanup is visible in its source or destination.
	for _, name := range []string{
		database.JourneysRawCollectionName,
		database.JourneysStagingCollectionName,
		database.JourneysCollectionName,
	} {
		if len(stale) == 0 {
			return nil
		}
		if err := excludeReferencedTracks(ctx, name, stale); err != nil {
			return fmt.Errorf("check track references in %s: %w", name, err)
		}
	}

	tracks := database.GetCollection("journey_tracks")
	batch := make([]string, 0, 500)
	for ref := range stale {
		batch = append(batch, ref)
		if len(batch) == cap(batch) {
			filter["primaryidentifier"] = bson.M{"$in": batch}
			if _, err := tracks.DeleteMany(ctx, filter); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		filter["primaryidentifier"] = bson.M{"$in": batch}
		_, err = tracks.DeleteMany(ctx, filter)
	}
	return err
}

func excludeReferencedTracks(ctx context.Context, collectionName string, stale map[string]struct{}) error {
	cursor, err := database.GetCollection(collectionName).Find(ctx, bson.M{
		"$or": bson.A{
			bson.M{"trackref": bson.M{"$exists": true}},
			bson.M{"path.trackref": bson.M{"$exists": true}},
		},
	}, options.Find().SetProjection(bson.M{"_id": 0, "trackref": 1, "path.trackref": 1}))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for len(stale) > 0 && cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return err
		}
		delete(stale, journey.TrackRef)
		for _, leg := range journey.Path {
			if leg != nil {
				delete(stale, leg.TrackRef)
			}
		}
	}
	return cursor.Err()
}
