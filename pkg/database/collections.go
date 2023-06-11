package database

import (
	"context"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

func createIndexes() {
	createStopsIndexes()
	createOperatorsIndexes()
	createJourneysIndexes()
}

func createStopsIndexes() {
	// Stops
	stopsCollection := GetCollection("stops")
	stopsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "location", Value: bsonx.String("2dsphere")}},
		},
		{
			Keys: bsonx.Doc{{Key: "platforms.primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "entrances.primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "associations.associatedidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "otheridentifiers.Tiploc", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "otheridentifiers.Crs", Value: bsonx.Int32(1)}},
		},
	}

	opts := options.CreateIndexes()
	_, err := stopsCollection.Indexes().CreateMany(context.Background(), stopsIndex, opts)
	if err != nil {
		panic(err)
	}

	// Stop Groups
	stopGroupsCollection := GetCollection("stop_groups")
	stopGroupsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "identifier", Value: bsonx.Int32(1)}},
		},
	}

	opts = options.CreateIndexes()
	_, err = stopGroupsCollection.Indexes().CreateMany(context.Background(), stopGroupsIndex, opts)
	if err != nil {
		panic(err)
	}
}

func createOperatorsIndexes() {
	// Operators
	operatorsCollection := GetCollection("operators")

	operatorIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "otheridentifiers", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "operatorgroupref", Value: bsonx.Int32(1)}},
		},
	}

	opts := options.CreateIndexes()
	_, err := operatorsCollection.Indexes().CreateMany(context.Background(), operatorIndex, opts)
	if err != nil {
		panic(err)
	}

	// OperatorGroups
	operatorGroupsCollection := GetCollection("operator_groups")
	operatorGroupsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "identifier", Value: bsonx.Int32(1)}},
		},
	}

	opts = options.CreateIndexes()
	_, err = operatorGroupsCollection.Indexes().CreateMany(context.Background(), operatorGroupsIndex, opts)
	if err != nil {
		panic(err)
	}
}

func createJourneysIndexes() {
	// Services
	servicesCollection := GetCollection("services")
	serviceNameOperatorRefIndexName := "ServiceNameOperatorRef"
	_, err := servicesCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "datasource.dataset", Value: bsonx.Int32(1)}},
		},
		{
			Options: &options.IndexOptions{
				Name: &serviceNameOperatorRefIndexName,
			},
			Keys: bson.D{
				{Key: "servicename", Value: 1},
				{Key: "operatorref", Value: 1},
			},
		},
	}, options.CreateIndexes())
	if err != nil {
		panic(err)
	}

	// Journeys
	journeysCollection := GetCollection("journeys")

	journeyIdentificationServiceOriginStopsIndexName := "JourneyIdentificationServiceOriginStops"
	journeyIdentificationServiceDestinationStopsIndexName := "JourneyIdentificationServiceDestinationStops"
	journeyIdentificationServiceTicketMachineJourneycodeIndexName := "JourneyIdentificationServiceTicketMachineJourneyCode"
	journeyIdentificationServiceBlockNumberIndexName := "JourneyIdentificationServiceBlockNumberJourneyCode"
	_, err = journeysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "serviceref", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "path.originstopref", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "path.destinationstopref", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "datasource.dataset", Value: bsonx.Int32(1)}},
		},
		{
			Options: &options.IndexOptions{
				Name: &journeyIdentificationServiceOriginStopsIndexName,
			},
			Keys: bson.D{
				{Key: "serviceref", Value: 1},
				{Key: "path.originstopref", Value: 1},
			},
		},
		{
			Options: &options.IndexOptions{
				Name: &journeyIdentificationServiceDestinationStopsIndexName,
			},
			Keys: bson.D{
				{Key: "serviceref", Value: 1},
				{Key: "path.destinationstopref", Value: 1},
			},
		},
		{
			Options: &options.IndexOptions{
				Name: &journeyIdentificationServiceTicketMachineJourneycodeIndexName,
			},
			Keys: bson.D{
				{Key: "serviceref", Value: 1},
				{Key: "otheridentifiers.TicketMachineJourneyCode", Value: 1},
			},
		},
		{
			Options: &options.IndexOptions{
				Name: &journeyIdentificationServiceBlockNumberIndexName,
			},
			Keys: bson.D{
				{Key: "serviceref", Value: 1},
				{Key: "otheridentifiers.BlockNumber", Value: 1},
			},
		},
	}, options.CreateIndexes())
	if err != nil {
		panic(err)
	}

	// RealtimeJourneys
	realtimeJourneysCollection := GetCollection("realtime_journeys")
	_, err = realtimeJourneysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "journey.primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "stops.$**", Value: bsonx.Int32(1)}},
		},
		{
			Keys:    bsonx.Doc{{Key: "modificationdatetime", Value: bsonx.Int32(1)}},
			Options: options.Index().SetExpireAfterSeconds(3600), // Expire after 1 hour
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err)
	}
}
