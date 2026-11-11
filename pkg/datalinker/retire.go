package datalinker

import (
	"context"
	"fmt"
	"sort"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/indexer"
	"github.com/travigo/travigo/pkg/journeytracks"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func removeRetiredDatasets(ctx context.Context) error {
	registered := manager.GetRegisteredDataSets()
	if len(registered) == 0 {
		return fmt.Errorf("cannot identify retired datasets with an empty datasource registry")
	}
	// Keep internal checkpoints and every configured ID, including datasets
	// excluded from the batch plan or from a manually selected import.
	keep := []string{"", datasetversion.LinkerDataset("stop"), datasetversion.LinkerDataset("service"),
		datasetversion.LinkerDataset("journey"), datasetversion.StopTransfersDataset}
	for _, dataset := range registered {
		keep = append(keep, dataset.Identifier)
	}

	versions := database.GetCollection("dataset_versions")
	reports := database.GetCollection("dataset_import_reports")
	retired := map[string]struct{}{}
	for _, source := range []struct {
		collection *mongo.Collection
		field      string
	}{
		{versions, "dataset"},
		{reports, "datasetidentifier"},
	} {
		values, err := source.collection.Distinct(ctx, source.field,
			bson.M{source.field: bson.M{"$type": "string", "$nin": keep}})
		if err != nil {
			return fmt.Errorf("find retired IDs in %s: %w", source.collection.Name(), err)
		}
		for _, value := range values {
			if id, ok := value.(string); ok {
				retired[id] = struct{}{}
			}
		}
	}
	if len(retired) == 0 {
		log.Info().Msg("No retired datasets found")
		return nil
	}
	if err := elastic_client.Connect(true); err != nil {
		return err
	}

	ids := make([]string, 0, len(retired))
	for id := range retired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	log.Info().Strs("datasets", ids).Msg("Removing retired dataset data")
	if err := deleteRetiredDatasetData(ctx, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := indexer.DeleteStopDatasetIndex(ctx, id); err != nil {
			return fmt.Errorf("delete retired dataset %q stop index: %w", id, err)
		}
		// Keep discovery records until every deletion has succeeded so the next
		// run can retry a partially completed cleanup.
		if _, err := reports.DeleteMany(ctx, bson.M{"datasetidentifier": id}); err != nil {
			return fmt.Errorf("delete retired dataset %q import reports: %w", id, err)
		}
		if _, err := versions.DeleteMany(ctx, bson.M{"dataset": id}); err != nil {
			return fmt.Errorf("delete retired dataset %q versions: %w", id, err)
		}
		log.Info().Str("dataset", id).Msg("Removed retired dataset")
	}
	return nil
}

func deleteRetiredDatasetData(ctx context.Context, ids []string) error {
	filter := bson.M{"datasource.datasetid": bson.M{"$in": ids}}
	for _, name := range []string{
		"stops_raw", "stop_groups", "operators", "operator_groups", "services_raw", "stop_transfers",
		database.JourneysRawCollectionName,
	} {
		if _, err := database.GetCollection(name).DeleteMany(ctx, filter); err != nil {
			return fmt.Errorf("delete data from %s: %w", name, err)
		}
	}
	if _, err := database.GetCollection(database.JourneysCollectionName).DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("delete published journeys: %w", err)
	}
	// Run this even when a retry finds no live journeys. The manifest may still
	// point to the previous generation if the prior attempt stopped after deletion.
	if err := stopservices.RetireDatasets(ctx, database.Instance.Database, ids); err != nil {
		return fmt.Errorf("retire stop-service contributions: %w", err)
	}
	if err := journeytracks.RemoveDatasets(ctx, ids); err != nil {
		return fmt.Errorf("remove journey tracks: %w", err)
	}
	// Save the rebuild request before removing discovery records. The transfer
	// builder waits for a stop topology published after the deletion.
	if err := requestStopTransferRebuild(ctx); err != nil {
		return err
	}
	return indexer.RequestStopIndexRebuild(ctx)
}
