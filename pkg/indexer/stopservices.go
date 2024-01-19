package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"io"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/kr/pretty"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
)

func IndexStopServices() {
	indexName := fmt.Sprintf("travigo-stopservices-%d", time.Now().Unix())

	createStopServicesIndex(indexName)
	indexStopServicesFromMongo(indexName)

	deleteOldIndexes("travigo-stopservices-*", indexName)
}

func createStopServicesIndex(indexName string) {
	indexReq := esapi.IndicesCreateRequest{
		Index: indexName,
	}

	resp, err := indexReq.Do(context.Background(), elastic_client.Client)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create index")
	}

	responseBytes, _ := io.ReadAll(resp.Body)
	pretty.Println(string(responseBytes))
}

type basicService struct {
	PrimaryIdentifier string `groups:"basic"`

	ServiceName string `groups:"basic"`

	OperatorRef string `groups:"basic"`

	BrandColour          string `groups:"basic"`
	SecondaryBrandColour string `groups:"basic"`
	BrandIcon            string `groups:"basic"`

	TransportType ctdf.TransportType `groups:"basic"`
}

func indexStopServicesFromMongo(indexName string) {
	stopsCollection := database.GetCollection("stops")

	cursor, _ := stopsCollection.Find(context.Background(), bson.M{})

	for cursor.Next(context.TODO()) {
		var stop *ctdf.Stop
		cursor.Decode(&stop)

		var services []*ctdf.Service
		services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
			Stop: stop,
		})

		for _, service := range services {
			indexObject := map[string]interface{}{
				"Stop": stop.PrimaryIdentifier,
				"Service": basicService{
					PrimaryIdentifier:    service.PrimaryIdentifier,
					ServiceName:          service.ServiceName,
					OperatorRef:          service.OperatorRef,
					BrandColour:          service.BrandColour,
					SecondaryBrandColour: service.SecondaryBrandColour,
					BrandIcon:            service.BrandIcon,
					TransportType:        service.TransportType,
				},
			}

			jsonStop, _ := json.Marshal(indexObject)

			elastic_client.IndexRequest(indexName, bytes.NewReader(jsonStop))
		}
	}

	log.Info().Msg("Sent all index requests to queue")
}
