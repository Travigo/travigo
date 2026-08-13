package dbwatch

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ServiceAlertsWatch struct {
	EventQueue rmq.Queue
}

type serviceAlertUpdateDescription struct {
	UpdatedFields bson.M   `bson:"updatedFields"`
	RemovedFields []string `bson:"removedFields"`
}

var serviceAlertContentFields = map[string]struct{}{
	"alerttype": {},
	"title":     {},
	"text":      {},
}

func serviceAlertUpdateHasField(description serviceAlertUpdateDescription, wanted string) bool {
	for field := range description.UpdatedFields {
		if strings.SplitN(field, ".", 2)[0] == wanted {
			return true
		}
	}

	for _, field := range description.RemovedFields {
		if strings.SplitN(field, ".", 2)[0] == wanted {
			return true
		}
	}

	return false
}

func serviceAlertUpdateHasContentChange(description serviceAlertUpdateDescription) bool {
	for field := range description.UpdatedFields {
		if _, ok := serviceAlertContentFields[strings.SplitN(field, ".", 2)[0]]; ok {
			return true
		}
	}

	for _, field := range description.RemovedFields {
		if _, ok := serviceAlertContentFields[strings.SplitN(field, ".", 2)[0]]; ok {
			return true
		}
	}

	return false
}

func serviceAlertAddedMatchedIdentifiers(before, after *ctdf.ServiceAlert) ([]string, bool) {
	if before == nil || after == nil {
		return nil, false
	}

	beforeIdentifiers := make(map[string]struct{}, len(before.MatchedIdentifiers))
	for _, identifier := range before.MatchedIdentifiers {
		beforeIdentifiers[identifier] = struct{}{}
	}

	afterIdentifiers := make(map[string]struct{}, len(after.MatchedIdentifiers))
	addedIdentifiers := make([]string, 0)
	for _, identifier := range after.MatchedIdentifiers {
		if _, alreadySeen := afterIdentifiers[identifier]; alreadySeen {
			continue
		}
		afterIdentifiers[identifier] = struct{}{}

		if _, existed := beforeIdentifiers[identifier]; !existed {
			addedIdentifiers = append(addedIdentifiers, identifier)
		}
	}

	return addedIdentifiers, serviceAlertIdentifierSetsDiffer(beforeIdentifiers, afterIdentifiers)
}

func serviceAlertIdentifierSetsDiffer(before, after map[string]struct{}) bool {
	if len(before) != len(after) {
		return true
	}

	for identifier := range before {
		if _, exists := after[identifier]; !exists {
			return true
		}
	}

	return false
}

func NewServiceAlertsWatch() *ServiceAlertsWatch {
	eventQueue, err := redis_client.QueueConnection.OpenQueue("events-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start event queue")
	}

	return &ServiceAlertsWatch{
		EventQueue: eventQueue,
	}
}

func (w *ServiceAlertsWatch) Run() {
	log.Info().Msg("Starting dbwatch on collection service_alerts")
	collection := database.GetCollection("service_alerts")
	if err := collection.Database().RunCommand(context.Background(), bson.D{
		{Key: "collMod", Value: "service_alerts"},
		{Key: "changeStreamPreAndPostImages", Value: bson.D{{Key: "enabled", Value: true}}},
	}).Err(); err != nil {
		log.Warn().Err(err).Msg("Could not enable ServiceAlert change-stream pre-images; identifier-only updates will be skipped")
	}
	matchPipeline := bson.D{
		{
			Key: "$match", Value: bson.D{
				{Key: "operationType", Value: bson.D{
					{Key: "$in", Value: bson.A{"insert", "update", "replace"}},
				}},
			},
		},
	}
	stream, err := collection.Watch(
		context.Background(),
		mongo.Pipeline{matchPipeline},
		options.ChangeStream().SetFullDocument(options.UpdateLookup).SetFullDocumentBeforeChange(options.WhenAvailable),
	)
	if err != nil {
		panic(err)
	}

	defer stream.Close(context.Background())

	for stream.Next(context.Background()) {
		var data struct {
			OperationType            string                        `bson:"operationType"`
			FullDocument             *ctdf.ServiceAlert            `bson:"fullDocument"`
			FullDocumentBeforeChange *ctdf.ServiceAlert            `bson:"fullDocumentBeforeChange"`
			UpdateDescription        serviceAlertUpdateDescription `bson:"updateDescription"`
		}
		if err := stream.Decode(&data); err != nil {
			log.Error().Err(err).Msg("Failed to decode event")
			continue
		}

		if data.FullDocument == nil {
			log.Warn().Str("operation", data.OperationType).Msg("ServiceAlert change had no full document")
			continue
		}

		contentChanged := serviceAlertUpdateHasContentChange(data.UpdateDescription)
		identifiersFieldChanged := serviceAlertUpdateHasField(data.UpdateDescription, "matchedidentifiers")
		if data.OperationType == "update" && !contentChanged && !identifiersFieldChanged {
			log.Debug().Str("id", data.FullDocument.PrimaryIdentifier).Msg("Skipping ServiceAlert update without notification-relevant changes")
			continue
		}

		if data.OperationType == "update" && identifiersFieldChanged {
			addedIdentifiers, identifiersChanged := serviceAlertAddedMatchedIdentifiers(data.FullDocumentBeforeChange, data.FullDocument)
			if data.FullDocumentBeforeChange == nil {
				if !contentChanged {
					log.Warn().Str("id", data.FullDocument.PrimaryIdentifier).Msg("Skipping ServiceAlert identifier-only update without a pre-image")
					continue
				}
			} else if !identifiersChanged {
				if !contentChanged {
					log.Debug().Str("id", data.FullDocument.PrimaryIdentifier).Msg("Skipping ServiceAlert update with unchanged identifiers")
					continue
				}
			} else if !contentChanged && len(addedIdentifiers) == 0 {
				log.Debug().Str("id", data.FullDocument.PrimaryIdentifier).Msg("Skipping ServiceAlert update that only removed identifiers")
				continue
			} else if !contentChanged {
				document := *data.FullDocument
				document.MatchedIdentifiers = addedIdentifiers
				data.FullDocument = &document
			}
		}

		log.Info().Str("id", data.FullDocument.PrimaryIdentifier).Str("operation", data.OperationType).Msg("ServiceAlert changed")

		eventBytes, _ := json.Marshal(ctdf.Event{
			Type:      ctdf.EventTypeServiceAlertCreated,
			Timestamp: time.Now(),
			Body:      data.FullDocument,
		})
		w.EventQueue.PublishBytes(eventBytes)
	}
}
