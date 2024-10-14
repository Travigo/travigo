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
	stopsCollection := database.GetCollection("stops")

	cursor, _ := stopsCollection.Find(context.Background(), bson.M{})

	for cursor.Next(context.Background()) {
		var stop *ctdf.Stop
		cursor.Decode(&stop)

		if !stop.Active {
			continue
		}

		var services []*ctdf.Service
		var basicServices []*basicService
		services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
			Stop: stop,
		})

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
	}

	log.Info().Msg("Sent all index requests to queue")
}
