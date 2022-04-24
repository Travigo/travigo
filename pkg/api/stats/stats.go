package stats

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type RecordsStats struct {
	Stops                      int64
	Operators                  int64
	Services                   int64
	ActiveRealtimeJourneys     int64
	HistoricalRealtimeJourneys int64
}

var CurrentRecordsStats *RecordsStats

func UpdateRecordsStats() {
	CurrentRecordsStats = &RecordsStats{
		Stops:                      0,
		Operators:                  0,
		Services:                   0,
		ActiveRealtimeJourneys:     0,
		HistoricalRealtimeJourneys: 0,
	}

	for {
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

		numberRealtimeJourneys, _ := realtimeJourneysCollection.CountDocuments(context.Background(), bson.D{})

		var numberActiveRealtimeJourneys int64
		realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
		activeRealtimeJourneys, _ := realtimeJourneysCollection.Find(context.Background(), bson.M{
			"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
		})
		for activeRealtimeJourneys.Next(context.TODO()) {
			var realtimeJourney *ctdf.RealtimeJourney
			activeRealtimeJourneys.Decode(&realtimeJourney)

			if realtimeJourney.IsActive() {
				numberActiveRealtimeJourneys += 1
			}
		}

		numberHistoricRealtimeJourneys := numberRealtimeJourneys - numberActiveRealtimeJourneys

		CurrentRecordsStats.ActiveRealtimeJourneys = numberActiveRealtimeJourneys
		CurrentRecordsStats.HistoricalRealtimeJourneys = numberHistoricRealtimeJourneys

		time.Sleep(1 * time.Minute)
	}
}
