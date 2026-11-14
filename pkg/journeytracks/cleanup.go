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
	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysStagingCollectionName, database.JourneysCollectionName} {
		collection := database.GetCollection(name)
		for batch := range slices.Chunk(refs, 5000) {
			trackRefs := bson.M{"$in": batch}
			if _, err := collection.UpdateMany(ctx, bson.M{"trackref": trackRefs},
				bson.M{"$unset": bson.M{"trackref": ""}}); err != nil {
				return fmt.Errorf("clear journey track references in %s: %w", name, err)
			}
			if _, err := collection.UpdateMany(ctx, bson.M{"calls.outgoing.trackref": trackRefs},
				bson.M{"$unset": bson.M{"calls.$[call].outgoing.trackref": ""}},
				options.Update().SetArrayFilters(options.ArrayFilters{Filters: []interface{}{
					bson.M{"call.outgoing.trackref": trackRefs},
				}})); err != nil {
				return fmt.Errorf("clear path track references in %s: %w", name, err)
			}
		}
	}
	if _, err := database.GetCollection(RouteCollectionName).DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("delete data from %s: %w", RouteCollectionName, err)
	}
	if err := excludeReferencedTracksAcrossCollections(ctx, owned); err != nil {
		return err
	}
	if err := deleteTrackRefs(ctx, owned, filter); err != nil {
		return err
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

	return cleanupStaleTracks(ctx, stale, filter)
}

// CleanupStaleUnreferencedTracks removes prior generations only after their
// geometry is no longer referenced by raw, staging, or published journeys and
// route metadata. A track still used by the currently published generation is
// retained; a later refresh/maintenance pass can remove it once publication has
// moved past that reference.
func CleanupStaleUnreferencedTracks(ctx context.Context, source *ctdf.DataSourceReference) error {
	if source == nil || source.DatasetID == "" {
		return nil
	}
	filter := bson.M{
		"datasource.datasetid":      source.DatasetID,
		"datasource.originalformat": source.OriginalFormat,
		"datasource.timestamp":      bson.M{"$ne": source.Timestamp},
	}
	stale, err := loadTrackRefs(ctx, filter)
	if err != nil {
		return err
	}
	return cleanupStaleTracks(ctx, stale, filter)
}

func cleanupStaleTracks(ctx context.Context, stale map[string]struct{}, ownerFilter bson.M) error {
	if err := excludeReferencedTracksAcrossCollections(ctx, stale); err != nil {
		return err
	}
	return deleteTrackRefs(ctx, stale, ownerFilter)
}

func excludeReferencedTracksAcrossCollections(ctx context.Context, stale map[string]struct{}) error {
	for _, name := range []string{
		database.JourneysRawCollectionName,
		database.JourneysStagingCollectionName,
		database.JourneysCollectionName,
		RouteCollectionName,
	} {
		if len(stale) == 0 {
			return nil
		}
		if err := excludeReferencedTracksInCollection(ctx, name, stale); err != nil {
			return fmt.Errorf("check track references in %s: %w", name, err)
		}
	}
	return nil
}

func excludeReferencedTracksInCollection(ctx context.Context, collectionName string, stale map[string]struct{}) error {
	refs := make([]string, 0, len(stale))
	for ref := range stale {
		refs = append(refs, ref)
	}
	collection := database.GetCollection(collectionName)
	for batch := range slices.Chunk(refs, 5000) {
		trackRefs := bson.M{"$in": batch}
		cursor, err := collection.Find(ctx, bson.M{"$or": bson.A{
			bson.M{"trackref": trackRefs},
			bson.M{"calls.outgoing.trackref": trackRefs},
		}}, options.Find().SetProjection(bson.M{"_id": 0, "trackref": 1, "calls.outgoing.trackref": 1}))
		if err != nil {
			return err
		}
		for cursor.Next(ctx) {
			var journey struct {
				TrackRef string `bson:"trackref"`
				Calls    []struct {
					Outgoing struct {
						TrackRef string `bson:"trackref"`
					} `bson:"outgoing"`
				} `bson:"calls"`
			}
			if err := cursor.Decode(&journey); err != nil {
				cursor.Close(ctx)
				return err
			}
			delete(stale, journey.TrackRef)
			for _, call := range journey.Calls {
				delete(stale, call.Outgoing.TrackRef)
			}
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return err
		}
		cursor.Close(ctx)
	}
	return nil
}

func deleteTrackRefs(ctx context.Context, refs map[string]struct{}, ownerFilter bson.M) error {
	tracks := database.GetCollection("journey_tracks")
	batch := make([]string, 0, 500)
	deleteBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		filter := bson.M{"primaryidentifier": bson.M{"$in": batch}}
		for key, value := range ownerFilter {
			filter[key] = value
		}
		if _, err := tracks.DeleteMany(ctx, filter); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for ref := range refs {
		batch = append(batch, ref)
		if len(batch) == cap(batch) {
			if err := deleteBatch(); err != nil {
				return err
			}
		}
	}
	return deleteBatch()
}
