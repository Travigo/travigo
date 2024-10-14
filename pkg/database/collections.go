package database

import (
	"context"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func createIndexes() {
	createStopsIndexes()
	createOperatorsIndexes()
	createJourneysIndexes()
	createRealtimeIndexes()
}

func createStopsIndexes() {
	// Stops
	stopsCollection := GetCollection("stops")
	stopsIndex := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "location.coordinates", Value: "2d"}},
		},
		{
			Keys: bson.D{{Key: "associations.associatedidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "otheridentifiers", Value: 1}},
		},
	}

	opts := options.CreateIndexes()
	_, err := stopsCollection.Indexes().CreateMany(context.Background(), stopsIndex, opts)
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// Stop Groups
	stopGroupsCollection := GetCollection("stop_groups")
	stopGroupsIndex := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "identifier", Value: 1}},
		},
	}

	opts = options.CreateIndexes()
	_, err = stopGroupsCollection.Indexes().CreateMany(context.Background(), stopGroupsIndex, opts)
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}
}

func createOperatorsIndexes() {
	// Operators
	operatorsCollection := GetCollection("operators")

	operatorIndex := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "otheridentifiers", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "operatorgroupref", Value: 1}},
		},
	}

	opts := options.CreateIndexes()
	_, err := operatorsCollection.Indexes().CreateMany(context.Background(), operatorIndex, opts)
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// OperatorGroups
	operatorGroupsCollection := GetCollection("operator_groups")
	operatorGroupsIndex := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "identifier", Value: 1}},
		},
	}

	opts = options.CreateIndexes()
	_, err = operatorGroupsCollection.Indexes().CreateMany(context.Background(), operatorGroupsIndex, opts)
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}
}

func createRealtimeIndexes() {
	// RealtimeJourneys
	realtimeJourneysCollection := GetCollection("realtime_journeys")
	_, err := realtimeJourneysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "journey.primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "stops.$**", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "otheridentifiers.nationalrailrid", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "otheridentifiers.TrainID", Value: 1}},
		},
		{
			Keys: bson.D{
				{Key: "datasource.provider", Value: 1},
				{Key: "datasource.datasetid", Value: 1},
				{Key: "datasource.timestamp", Value: 1},
			},
		},
		{
			Keys:    bson.D{{Key: "modificationdatetime", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(4 * 3600), // Expire after 4 hours
		},
		{
			Keys: bson.D{{Key: "activelytracked", Value: 1}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}
}

func createJourneysIndexes() {
	// Services
	servicesCollection := GetCollection("services")
	serviceNameOperatorRefIndexName := "ServiceNameOperatorRef"
	_, err := servicesCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "datasource.datasetid", Value: 1}},
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
		log.Error().Err(err).Msg("Creating Index")
	}

	// Journeys
	journeysCollection := GetCollection("journeys")

	// journeyIdentificationServiceOriginStopsIndexName := "JourneyIdentificationServiceOriginStops"
	// journeyIdentificationServiceDestinationStopsIndexName := "JourneyIdentificationServiceDestinationStops"
	journeyIdentificationServiceTicketMachineJourneycodeIndexName := "JourneyIdentificationServiceTicketMachineJourneyCode"
	journeyIdentificationServiceBlockNumberIndexName := "JourneyIdentificationServiceBlockNumberJourneyCode"
	_, err = journeysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "serviceref", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "path.originstopref", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "path.destinationstopref", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "datasource.datasetid", Value: 1}},
		},
		// {
		// 	Options: &options.IndexOptions{
		// 		Name: &journeyIdentificationServiceOriginStopsIndexName,
		// 	},
		// 	Keys: bson.D{
		// 		{Key: "serviceref", Value: 1},
		// 		{Key: "path.originstopref", Value: 1},
		// 	},
		// },
		// {
		// 	Options: &options.IndexOptions{
		// 		Name: &journeyIdentificationServiceDestinationStopsIndexName,
		// 	},
		// 	Keys: bson.D{
		// 		{Key: "serviceref", Value: 1},
		// 		{Key: "path.destinationstopref", Value: 1},
		// 	},
		// },
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
		{
			Keys: bson.D{{Key: "otheridentifiers.TrainUID", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "otheridentifiers.GTFS-TripID", Value: 1}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// Retry Records
	retryRecordsCollection := GetCollection("retry_records")
	_, err = retryRecordsCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "creationdatetime", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(2 * 3600), // Expire after 2 hours
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// ServiceAlerts
	serviceAlertsCollection := GetCollection("service_alerts")
	_, err = serviceAlertsCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "primaryidentifier", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "matchedidentifiers", Value: 1}},
		},
		{
			Keys:    bson.D{{Key: "validuntil", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(32 * 3600), // Expire after 32 hours
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// UserPushNotificationTarget
	userPushNotificationTargetCollection := GetCollection("user_push_notification_target")
	_, err = userPushNotificationTargetCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "userid", Value: 1}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// UserEventNotificationExpression
	userEventSubscriptionCollection := GetCollection("user_event_subscription")
	_, err = userEventSubscriptionCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "userid", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "eventtype", Value: 1}},
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}

	// UserPushNotificationTarget
	tflTrackerCollection := GetCollection("tfl_tracker")
	_, err = tflTrackerCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "creationdatetime", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(4 * 3600), // Expire after 4 hours
		},
	}, options.CreateIndexes())
	if err != nil {
		log.Error().Err(err).Msg("Creating Index")
	}
}
