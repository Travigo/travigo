package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/kr/pretty"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
)

func IndexStops() {
	indexName := fmt.Sprintf("travigo-stops-%d", time.Now().Unix())

	createStopIndex(indexName)
	indexStopsFromMongo(indexName)

	deleteOldIndexes("travigo-stops-*", indexName)
}

func createStopIndex(indexName string) {
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
		Body:  strings.NewReader(string(mapping)),
	}

	resp, err := indexReq.Do(context.Background(), elastic_client.Client)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create index")
	}

	responseBytes, _ := io.ReadAll(resp.Body)
	pretty.Println(string(responseBytes))
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

func indexStopsFromMongo(indexName string) {
	now := time.Now()
	stopsCollection := database.GetCollection("stops")

	cursor, err := stopsCollection.Find(context.Background(), bson.M{})
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch stops for indexing")
		return
	}

	totalStops := 0
	inactiveSkipped := 0
	railInactiveSkipped := 0
	indexedStops := 0
	railIndexedStops := 0

	for cursor.Next(context.Background()) {
		var stop *ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode stop for indexing")
			continue
		}

		totalStops++

		if !stop.Active {
			inactiveSkipped++
			if stopHasTransportType(stop, ctdf.TransportTypeRail) {
				railInactiveSkipped++
			}
			continue
		}

		var services []*ctdf.Service
		var basicServices []*basicService
		services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
			Stop: stop,
		})

		// Force a filling of the cache of the stops journeys for rail only - TODO a bit of a hack
		if len(stop.TransportTypes) == 1 && stop.TransportTypes[0] == ctdf.TransportTypeRail {
			go func() {
				dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
					Stop:          stop,
					Count:         1,
					StartDateTime: now,
				})
			}()
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

		jsonStop, _ := json.Marshal(map[string]interface{}{
			"PrimaryIdentifier": stop.PrimaryIdentifier,
			"OtherIdentifiers":  stop.OtherIdentifiers,
			"PrimaryName":       stop.PrimaryName,
			"Descriptor":        stop.Descriptor,
			"TransportTypes":    stop.TransportTypes,
			"Location":          stop.Location,
			"Services":          basicServices,
		})

		elastic_client.IndexRequest(indexName, bytes.NewReader(jsonStop))
		indexedStops++
		if stopHasTransportType(stop, ctdf.TransportTypeRail) {
			railIndexedStops++
		}
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Msg("Failed while iterating stops for indexing")
	}

	log.Info().
		Int("total_stops", totalStops).
		Int("indexed_stops", indexedStops).
		Int("rail_indexed_stops", railIndexedStops).
		Int("inactive_skipped", inactiveSkipped).
		Int("rail_inactive_skipped", railInactiveSkipped).
		Msg("Sent stop index requests to queue")
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
