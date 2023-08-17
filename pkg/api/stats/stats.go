package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"go.mongodb.org/mongo-driver/bson"
)

type RecordsStats struct {
	Stops                  int64
	Operators              int64
	Services               int64
	ActiveRealtimeJourneys RecordsStatsActiveRealtimeJourneys
}
type RecordsStatsActiveRealtimeJourneys struct {
	Current              int64
	LocationWithTrack    int64
	LocationWithoutTrack int64
	ExternalProvided     int64

	TransportTypes map[ctdf.TransportType]int
}
type recordStatsElasticEvent struct {
	Stats *RecordsStats

	Timestamp time.Time
}

var CurrentRecordsStats *RecordsStats

func UpdateRecordsStats() {
	CurrentRecordsStats = &RecordsStats{
		Stops:     0,
		Operators: 0,
		Services:  0,
		ActiveRealtimeJourneys: RecordsStatsActiveRealtimeJourneys{
			Current:              0,
			LocationWithTrack:    0,
			LocationWithoutTrack: 0,
			ExternalProvided:     0,

			TransportTypes: map[ctdf.TransportType]int{},
		},
	}

	for {
		startTime := time.Now()

		stopsCollection := database.GetCollection("stops")
		numberStops, _ := stopsCollection.CountDocuments(context.Background(), bson.D{})
		CurrentRecordsStats.Stops = numberStops

		operatorsCollection := database.GetCollection("operators")
		numberOperators, _ := operatorsCollection.CountDocuments(context.Background(), bson.D{})
		CurrentRecordsStats.Operators = numberOperators

		servicesCollection := database.GetCollection("services")
		numberServices, _ := servicesCollection.CountDocuments(context.Background(), bson.D{})
		CurrentRecordsStats.Services = numberServices

		realtimeJourneysCollection := database.GetCollection("realtime_journeys")

		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - get basic")

		var numberActiveRealtimeJourneys int64
		var numberActiveRealtimeJourneysWithTrack int64
		var numberActiveRealtimeJourneysWithoutTrack int64
		var numberActiveRealtimeJourneysExternal int64
		transportTypes := map[ctdf.TransportType]int{}

		realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
		startTime = time.Now()

		matchStage := bson.D{
			{
				Key: "$match",
				Value: bson.D{
					{Key: "modificationdatetime", Value: bson.M{"$gt": realtimeActiveCutoffDate}},
				},
			},
		}
		//lookupStage := bson.D{
		//	{
		//		"$lookup",
		//		bson.M{
		//			"from":         "services",
		//			"localField":   "journey.serviceref",
		//			"foreignField": "primaryidentifier",
		//			"as":           "_journeyservice",
		//		},
		//	},
		//}
		//addFieldStage := bson.D{
		//	{
		//		"$addFields",
		//		bson.M{"journey.service": bson.M{"$first": "$_journeyservice"}},
		//	},
		//}
		projectStage := bson.D{
			{
				Key: "$project",
				Value: bson.D{
					bson.E{Key: "primaryidentifier", Value: 1},
					bson.E{Key: "modificationdatetime", Value: 1},
					bson.E{Key: "reliability", Value: 1},
					bson.E{Key: "journey.serviceref", Value: 1},
					bson.E{Key: "vehiclelocation", Value: 1},
					bson.E{Key: "journey.path.destinationstopref", Value: 1},
					bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
				},
			},
		}
		activeRealtimeJourneys, _ := realtimeJourneysCollection.Aggregate(context.TODO(), mongo.Pipeline{matchStage, projectStage})
		var realtimeJourneys []ctdf.RealtimeJourney
		activeRealtimeJourneys.All(context.Background(), &realtimeJourneys)

		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - get all realtime journeys")
		startTime = time.Now()

		for _, realtimeJourney := range realtimeJourneys {
			if realtimeJourney.IsActive() {
				numberActiveRealtimeJourneys += 1

				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithTrack {
					numberActiveRealtimeJourneysWithTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityLocationWithoutTrack {
					numberActiveRealtimeJourneysWithoutTrack += 1
				}
				if realtimeJourney.Reliability == ctdf.RealtimeJourneyReliabilityExternalProvided {
					numberActiveRealtimeJourneysExternal += 1
				}

				realtimeJourney.Journey.GetService()
				if realtimeJourney.Journey.Service != nil {
					transportTypes[realtimeJourney.Journey.Service.TransportType] += 1
				}
			}
		}
		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - iterate over realtime journeys")

		CurrentRecordsStats.ActiveRealtimeJourneys.Current = numberActiveRealtimeJourneys
		CurrentRecordsStats.ActiveRealtimeJourneys.LocationWithTrack = numberActiveRealtimeJourneysWithTrack
		CurrentRecordsStats.ActiveRealtimeJourneys.LocationWithoutTrack = numberActiveRealtimeJourneysWithoutTrack
		CurrentRecordsStats.ActiveRealtimeJourneys.ExternalProvided = numberActiveRealtimeJourneysExternal
		CurrentRecordsStats.ActiveRealtimeJourneys.TransportTypes = transportTypes

		// Publish stats to Elasticsearch
		elasticEvent, _ := json.Marshal(&recordStatsElasticEvent{
			Stats:     CurrentRecordsStats,
			Timestamp: time.Now(),
		})

		elastic_client.IndexRequest("overall-stats-1", bytes.NewReader(elasticEvent))

		time.Sleep(10 * time.Minute)
	}
}
