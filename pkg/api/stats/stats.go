package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"go.mongodb.org/mongo-driver/bson"
)

type RecordsStats struct {
	GeneratedAt time.Time

	Stops                  int64
	Operators              int64
	Services               int64
	ServiceAlerts          int64
	ActiveRealtimeJourneys RecordsStatsActiveRealtimeJourneys
}
type RecordsStatsActiveRealtimeJourneys struct {
	Current              int64
	LocationWithTrack    int64
	LocationWithoutTrack int64
	ExternalProvided     int64

	NotActivelyTracked int64

	TransportTypes map[ctdf.TransportType]int
	Features       map[string]int
	Datasources    map[string]int
}
type recordStatsElasticEvent struct {
	Stats *RecordsStats

	Timestamp time.Time
}

var CurrentRecordsStats *RecordsStats

func UpdateRecordsStats() {
	tflBusRegex := regexp.MustCompile("gb-tfl-line\\/[0-9]+\\/arrivals")
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
			Features:       map[string]int{},
		},
	}

	for {
		startTime := time.Now()

		stopsCollection := database.GetCollection("stops_raw")
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
		var numberActiveRealtimeJourneysNotActivelyTracked int64
		transportTypes := map[ctdf.TransportType]int{}
		features := map[string]int{}
		datasources := map[string]int{}

		realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
		startTime = time.Now()

		matchStage := bson.D{
			{
				Key: "$match",
				Value: bson.D{
					{Key: "modificationdatetime", Value: bson.M{"$gt": realtimeActiveCutoffDate}},
					{Key: "activelytracked", Value: true},
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
					bson.E{Key: "activelytracked", Value: 1},
					bson.E{Key: "timeoutdurationminutes", Value: 1},
					bson.E{Key: "modificationdatetime", Value: 1},
					bson.E{Key: "reliability", Value: 1},
					bson.E{Key: "datasource.datasetid", Value: 1},
					bson.E{Key: "service.transporttype", Value: 1},
					// bson.E{Key: "vehiclelocation", Value: 1},
					bson.E{Key: "journey.path.destinationstopref", Value: 1},
					bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
					bson.E{Key: "occupancy.occupancyavailable", Value: 1},
					bson.E{Key: "occupancy.actualvalues", Value: 1},
					bson.E{Key: "detailedrailinformation.carriages.id", Value: 1},
				},
			},
		}
		activeRealtimeJourneys, _ := realtimeJourneysCollection.Aggregate(context.Background(), mongo.Pipeline{matchStage, projectStage})

		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - get all realtime journeys")
		startTime = time.Now()

		for activeRealtimeJourneys.Next(context.Background()) {
			var realtimeJourney ctdf.RealtimeJourney
			err := activeRealtimeJourneys.Decode(&realtimeJourney)

			if err != nil {
				continue
			}

			if realtimeJourney.IsActive() {
				if realtimeJourney.ActivelyTracked {
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

					if realtimeJourney.Service != nil {
						transportTypes[realtimeJourney.Service.TransportType] += 1
					}

					datasetID := realtimeJourney.DataSource.DatasetID
					if tflBusRegex.MatchString(datasetID) {
						datasetID = "gb-tfl-line/bus/arrivals"
					}

					datasources[datasetID] += 1

					// Features
					if realtimeJourney.Occupancy.OccupancyAvailable {
						features["Occupancy"] += 1

						if realtimeJourney.Occupancy.ActualValues {
							features["OccupancyActualValues"] += 1
						}
					}
					if len(realtimeJourney.DetailedRailInformation.Carriages) > 0 {
						features["DetailedRailInformationCarriages"] += 1
					}
				} else {
					numberActiveRealtimeJourneysNotActivelyTracked += 1
				}
			}
		}
		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - iterate over realtime journeys")

		CurrentRecordsStats.ActiveRealtimeJourneys.Current = numberActiveRealtimeJourneys
		CurrentRecordsStats.ActiveRealtimeJourneys.LocationWithTrack = numberActiveRealtimeJourneysWithTrack
		CurrentRecordsStats.ActiveRealtimeJourneys.LocationWithoutTrack = numberActiveRealtimeJourneysWithoutTrack
		CurrentRecordsStats.ActiveRealtimeJourneys.ExternalProvided = numberActiveRealtimeJourneysExternal
		CurrentRecordsStats.ActiveRealtimeJourneys.TransportTypes = transportTypes
		CurrentRecordsStats.ActiveRealtimeJourneys.Features = features
		CurrentRecordsStats.ActiveRealtimeJourneys.Datasources = datasources
		CurrentRecordsStats.ActiveRealtimeJourneys.NotActivelyTracked = numberActiveRealtimeJourneysNotActivelyTracked

		numberServiceAlerts := 0
		startTime = time.Now()
		collection := database.GetCollection("service_alerts")

		now := time.Now()

		cursor, _ := collection.Find(context.Background(), bson.M{})
		for cursor.Next(context.Background()) {
			var serviceAlert ctdf.ServiceAlert
			err := cursor.Decode(&serviceAlert)
			if err != nil {
				log.Error().Err(err).Msg("Failed to decode ServiceAlert")
			}

			if serviceAlert.IsValid(now) {
				numberServiceAlerts += 1
			}
		}
		log.Debug().Str("Length", time.Now().Sub(startTime).String()).Msg("Stats - iterate over service alerts")
		CurrentRecordsStats.ServiceAlerts = int64(numberServiceAlerts)

		CurrentRecordsStats.GeneratedAt = time.Now()

		// Publish stats to Elasticsearch
		elasticEvent, _ := json.Marshal(&recordStatsElasticEvent{
			Stats:     CurrentRecordsStats,
			Timestamp: time.Now(),
		})

		elastic_client.IndexRequest("overall-stats-1", bytes.NewReader(elasticEvent))

		time.Sleep(time.Duration(18+rand.IntN(5)) * time.Minute)
	}
}
