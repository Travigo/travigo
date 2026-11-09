package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/elastic/go-elasticsearch/v8/esutil"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
)

func IndexStops(ctx context.Context) error {
	versions := database.GetCollection("dataset_versions")
	for _, dataset := range manager.GetRegisteredDataSets() {
		if !dataset.SupportedObjects.Stops ||
			dataset.ImportDestination == datasets.ImportDestinationRealtimeQueue ||
			dataset.ImportDestination == datasets.ImportDestinationSpecificRunner {
			continue
		}
		if err := indexStopDataset(ctx, versions, dataset.Identifier); err != nil {
			return fmt.Errorf("index stop dataset %s: %w", dataset.Identifier, err)
		}
	}
	return nil
}

func indexStopDataset(ctx context.Context, versions *mongo.Collection, datasetID string) error {
	filter := bson.M{"dataset": datasetID}
	var source ctdf.DatasetVersion
	err := versions.FindOne(ctx, filter).Decode(&source)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load dataset version: %w", err)
	}

	indexName := stopDatasetIndexName(datasetID)
	// This comparison tracks the stop import. Service and journey dependencies
	// embedded in the index have their own refresh lifecycle.
	if !source.IndexedStopsVersion.IsZero() && !source.LastModified.After(source.IndexedStopsVersion) {
		exists, err := indexExists(ctx, indexName)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}

		// The rebuild will create the index before it finishes writing. Clear
		// the old checkpoint so a retry cannot mistake a partial index for success.
		if _, err := versions.UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"indexedstopsversion": ""}}); err != nil {
			return fmt.Errorf("clear checkpoint for missing index: %w", err)
		}
	}

	log.Info().Str("dataset", datasetID).Time("version", source.LastModified).Msg("Indexing stop dataset")
	if err := rebuildStopIndex(ctx, indexName, datasetID); err != nil {
		return err
	}
	result, err := versions.UpdateOne(ctx, filter, bson.M{"$max": bson.M{"indexedstopsversion": source.LastModified}})
	if err != nil {
		return fmt.Errorf("save stop index checkpoint: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("save stop index checkpoint: source version disappeared")
	}
	log.Info().Str("dataset", datasetID).Msg("Indexed stop dataset")
	return nil
}

func stopDatasetIndexName(dataset string) string {
	return fmt.Sprintf("travigo-stops-%s", dataset)
}

func indexExists(ctx context.Context, indexName string) (bool, error) {
	request := esapi.IndicesExistsRequest{Index: []string{indexName}}
	resp, err := request.Do(ctx, elastic_client.Client)
	if err != nil {
		return false, fmt.Errorf("check stop index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return false, nil
	}
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("check stop index: %s", resp.Status())
	}
	return true, nil
}

func deleteIndex(ctx context.Context, indexName string) error {
	request := esapi.IndicesDeleteRequest{Index: []string{indexName}}
	resp, err := request.Do(ctx, elastic_client.Client)
	if err != nil {
		return fmt.Errorf("delete stop index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 && resp.IsError() {
		return fmt.Errorf("delete stop index: %s", resp.Status())
	}
	return nil
}

func deleteLegacyStopIndexes(ctx context.Context) {
	resp, err := elastic_client.Client.Cat.Indices(
		elastic_client.Client.Cat.Indices.WithContext(ctx),
		elastic_client.Client.Cat.Indices.WithIndex("travigo-stops-*"),
		elastic_client.Client.Cat.Indices.WithFormat("json"),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list legacy stop indexes")
		return
	}
	defer resp.Body.Close()

	var indexes []struct {
		Index string `json:"index"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&indexes); err != nil {
		log.Error().Err(err).Msg("Failed to decode stop indexes")
		return
	}

	legacyPattern := regexp.MustCompile(`^travigo-stops-[0-9]+$`)
	for _, index := range indexes {
		if legacyPattern.MatchString(index.Index) {
			if err := deleteIndex(ctx, index.Index); err != nil {
				log.Error().Err(err).Str("index", index.Index).Msg("Failed to delete legacy stop index")
			}
		}
	}
}

func createStopIndex(ctx context.Context, indexName string) error {
	// Performance Review [Set N] [PR096] [P2] (indexing): searchStops queries
	// OtherIdentifiers.search_as_you_type, but this mapping creates only text and
	// keyword fields there; identifier-prefix searches cannot use the requested
	// field. Align mapping and query, rebuild existing indexes, and verify both name
	// and identifier lookup against the actual mapping before publishing it.
	mapping := `{
		"settings": {
			"number_of_shards": 1,
			"number_of_replicas": 1
		},
		"mappings": {
			"properties": {
				"Location": {
					"properties": {
						"coordinates": {
							"type": "float"
						},
						"type": {
							"type": "text",
							"fields": {
								"keyword": {
									"type": "keyword",
									"ignore_above": 256
								}
							}
						}
					}
				},
				"OtherIdentifiers": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				},
				"OtherNames": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				},
				"PrimaryIdentifier": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				},
				"PrimaryName": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						},
						"search_as_you_type": {
							"type": "search_as_you_type"
						}
					}
				},
				"TransportTypes": {
					"type": "text",
					"fields": {
						"keyword": {
							"type": "keyword",
							"ignore_above": 256
						}
					}
				}
			}
		}
	}`

	indexReq := esapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(mapping),
	}

	resp, err := indexReq.Do(ctx, elastic_client.Client)
	if err != nil {
		return fmt.Errorf("create stop index: %w", err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return fmt.Errorf("create stop index: %s", resp.String())
	}
	var result struct {
		Acknowledged bool `json:"acknowledged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode stop index creation result: %w", err)
	}
	if !result.Acknowledged {
		return fmt.Errorf("stop index creation was not acknowledged")
	}
	return nil
}

func rebuildStopIndex(ctx context.Context, indexName, datasetID string) error {
	// Stop search uses the dataset index directly. Replacing it in place
	// preserves that contract, but it can be partial until the rebuild finishes.
	if err := deleteIndex(ctx, indexName); err != nil {
		return err
	}
	if err := createStopIndex(ctx, indexName); err != nil {
		return err
	}

	bulk, err := esutil.NewBulkIndexer(esutil.BulkIndexerConfig{
		Client:        elastic_client.Client,
		Index:         indexName,
		FlushInterval: 15 * time.Second,
		OnError: func(_ context.Context, err error) {
			log.Error().Err(err).Str("index", indexName).Msg("Stop index bulk request failed")
		},
	})
	if err != nil {
		return fmt.Errorf("create stop bulk indexer: %w", err)
	}
	indexErr := indexStopsFromMongo(ctx, datasetID, bulk)
	closeErr := bulk.Close(ctx)
	if err := errors.Join(indexErr, closeErr); err != nil {
		return err
	}
	stats := bulk.Stats()
	if stats.NumFailed != 0 || stats.NumIndexed != stats.NumAdded {
		return fmt.Errorf("stop index bulk writes incomplete: indexed %d of %d stops (%d failed)", stats.NumIndexed, stats.NumAdded, stats.NumFailed)
	}

	refresh := esapi.IndicesRefreshRequest{Index: []string{indexName}}
	resp, err := refresh.Do(ctx, elastic_client.Client)
	if err != nil {
		return fmt.Errorf("refresh stop index: %w", err)
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return fmt.Errorf("refresh stop index: %s", resp.String())
	}
	var result struct {
		Shards struct {
			Successful int `json:"successful"`
			Failed     int `json:"failed"`
		} `json:"_shards"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode stop index refresh result: %w", err)
	}
	if result.Shards.Failed > 0 || result.Shards.Successful == 0 {
		return fmt.Errorf("stop index refresh incomplete: %d successful shards, %d failed", result.Shards.Successful, result.Shards.Failed)
	}
	return nil
}

type basicService struct {
	PrimaryIdentifier    string
	ServiceName          string
	OperatorRef          string
	BrandColour          string
	SecondaryBrandColour string
	BrandIcon            string
	TransportType        ctdf.TransportType
}

const railDepartureBoardWarmConcurrency = 4

func indexStopsFromMongo(ctx context.Context, datasetID string, bulk esutil.BulkIndexer) error {
	now := time.Now()
	stopsCollection := database.GetCollection("stops")

	cursor, err := stopsCollection.Find(ctx, bson.M{"datasource.datasetid": datasetID})
	if err != nil {
		return fmt.Errorf("fetch stops: %w", err)
	}
	defer cursor.Close(ctx)

	railWarmJobs := make(chan *ctdf.Stop, railDepartureBoardWarmConcurrency)
	var railWarmGroup sync.WaitGroup
	var railWarmRequested atomic.Int64
	var railWarmCompleted atomic.Int64
	var railWarmErrors atomic.Int64
	for worker := 0; worker < railDepartureBoardWarmConcurrency; worker++ {
		railWarmGroup.Add(1)
		go func() {
			defer railWarmGroup.Done()
			for stop := range railWarmJobs {
				_, err := dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
					Stop:          stop,
					Count:         1,
					StartDateTime: now,
				})
				if err != nil {
					railWarmErrors.Add(1)
					log.Warn().Err(err).Str("stop", stop.PrimaryIdentifier).Msg("Failed to warm rail departure board cache")
					continue
				}
				railWarmCompleted.Add(1)
			}
		}()
	}

	totalStops := 0
	inactiveSkipped := 0
	railInactiveSkipped := 0
	indexedStops := 0
	railIndexedStops := 0

	defer func() {
		close(railWarmJobs)
		railWarmGroup.Wait()
		log.Info().
			Int("total_stops", totalStops).
			Int("indexed_stops", indexedStops).
			Int("rail_indexed_stops", railIndexedStops).
			Int("inactive_skipped", inactiveSkipped).
			Int("rail_inactive_skipped", railInactiveSkipped).
			Int64("rail_cache_warm_requested", railWarmRequested.Load()).
			Int64("rail_cache_warm_completed", railWarmCompleted.Load()).
			Int64("rail_cache_warm_errors", railWarmErrors.Load()).
			Msg("Scanned stops for indexing")
	}()

	for cursor.Next(ctx) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return fmt.Errorf("decode stop: %w", err)
		}

		totalStops++

		if !stop.Active {
			inactiveSkipped++
			if stopHasTransportType(&stop, ctdf.TransportTypeRail) {
				railInactiveSkipped++
			}
			continue
		}

		services, err := dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{Stop: &stop})
		if err != nil {
			return fmt.Errorf("load services for stop %s: %w", stop.PrimaryIdentifier, err)
		}
		var basicServices []*basicService

		// Force a filling of the cache of the stops journeys for rail only - TODO a bit of a hack
		// Performance Review [Set N] [PR098] [P2] (indexing): Sending to this small warm-up queue
		// blocks the Mongo cursor and all subsequent index production when boards are
		// slow. Count=1 still runs full board/realtime/estimate work and can trigger
		// graph-to-Mongo fallback. Decouple optional warming from successful index
		// publication and warm only versioned static cache entries with a time budget.
		if len(stop.TransportTypes) == 1 && stop.TransportTypes[0] == ctdf.TransportTypeRail {
			railWarmRequested.Add(1)
			select {
			case railWarmJobs <- &stop:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		for _, service := range services {
			basicServices = append(basicServices, &basicService{
				PrimaryIdentifier:    service.PrimaryIdentifier,
				ServiceName:          service.ServiceName,
				OperatorRef:          service.OperatorRef,
				BrandColour:          service.BrandColour,
				SecondaryBrandColour: service.SecondaryBrandColour,
				BrandIcon:            service.BrandIcon,
				TransportType:        service.TransportType,
			})
		}

		jsonStop, err := json.Marshal(map[string]interface{}{
			"PrimaryIdentifier": stop.PrimaryIdentifier,
			"OtherIdentifiers":  stop.OtherIdentifiers,
			"PrimaryName":       stop.PrimaryName,
			"Descriptor":        stop.Descriptor,
			"TransportTypes":    stop.TransportTypes,
			"Location":          stop.Location,
			"Services":          basicServices,
		})

		if err != nil {
			return fmt.Errorf("encode stop %s: %w", stop.PrimaryIdentifier, err)
		}
		err = bulk.Add(ctx, esutil.BulkIndexerItem{
			DocumentID: stop.PrimaryIdentifier,
			Action:     "index",
			Body:       bytes.NewReader(jsonStop),
			OnFailure: func(_ context.Context, item esutil.BulkIndexerItem, response esutil.BulkIndexerResponseItem, err error) {
				log.Error().Err(err).Str("stop", item.DocumentID).Str("reason", response.Error.Reason).Msg("Failed to index stop")
			},
		})
		if err != nil {
			return fmt.Errorf("queue stop %s: %w", stop.PrimaryIdentifier, err)
		}
		indexedStops++
		if stopHasTransportType(&stop, ctdf.TransportTypeRail) {
			railIndexedStops++
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate stops: %w", err)
	}
	return nil
}

func stopHasTransportType(stop *ctdf.Stop, transportType ctdf.TransportType) bool {
	if stop == nil {
		return false
	}

	for _, stopTransportType := range stop.TransportTypes {
		if stopTransportType == transportType {
			return true
		}
	}

	return false
}
