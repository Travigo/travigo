package nationalrail

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PushPortData struct {
	TrainStatuses []TrainStatus
}

func (p *PushPortData) UpdateRealtimeJourneys() {
	now := time.Now()
	datasource := &ctdf.DataSource{
		OriginalFormat: "DarwinPushPort",
		Provider:       "National-Rail",
		Dataset:        "DarwinPushPort",
		Identifier:     now.String(),
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	journeysCollection := database.GetCollection("journeys")

	operations := []mongo.WriteModel{}

	for _, trainStatus := range p.TrainStatuses {
		realtimeJourneyID := fmt.Sprintf("GB:DARWIN:%s:%s", trainStatus.SSD, trainStatus.UID)
		searchQuery := bson.M{"primaryidentifier": realtimeJourneyID}

		var realtimeJourney *ctdf.RealtimeJourney

		realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

		newRealtimeJourney := false
		if realtimeJourney == nil {
			// Find the journey for this train
			var journey *ctdf.Journey
			cursor, _ := journeysCollection.Find(context.Background(), bson.M{"otheridentifiers.TrainUID": trainStatus.UID})

			journeyDate, _ := time.Parse("2006-01-02", trainStatus.SSD)

			for cursor.Next(context.TODO()) {
				var potentialJourney *ctdf.Journey
				err := cursor.Decode(&potentialJourney)
				if err != nil {
					log.Error().Err(err).Msg("Failed to decode Journey")
				}

				if potentialJourney.Availability.MatchDate(journeyDate) {
					journey = potentialJourney
				}
			}

			if journey == nil {
				log.Error().Str("uid", trainStatus.UID).Msg("Failed to find respective Journey for this train")
				continue
			}

			// Construct the base realtime journey
			realtimeJourney = &ctdf.RealtimeJourney{
				PrimaryIdentifier: realtimeJourneyID,
				CreationDateTime:  now,
				Reliability:       ctdf.RealtimeJourneyReliabilityExternalProvided,

				DataSource: datasource,

				Journey: journey,

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}

			newRealtimeJourney = true
		}

		updateMap := bson.M{
			"modificationdatetime": now,
		}

		// Update database
		if newRealtimeJourney {
			updateMap["primaryidentifier"] = realtimeJourney.PrimaryIdentifier

			updateMap["reliability"] = realtimeJourney.Reliability

			updateMap["creationdatetime"] = realtimeJourney.CreationDateTime
			updateMap["datasource"] = realtimeJourney.DataSource

			updateMap["journey"] = realtimeJourney.Journey
		} else {
			updateMap["datasource.identifier"] = datasource.Identifier
		}

		for _, location := range trainStatus.Locations {
			stopID := fmt.Sprintf("GB:ATCO:%s", location.TPL)

			// if realtimeJourney.Stops[stopID] == nil {
			// 	updateMap[fmt.Sprintf("stops.%s", stopID)] = &ctdf.RealtimeJourneyStops{
			// 		StopRef:  stopID,
			// 		TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			// 	}
			// }

			if location.Arrival != nil {
				arrivalTime, _ := time.Parse("15:04", location.Arrival.ET)

				updateMap[fmt.Sprintf("stops.%s.arrivaltime", stopID)] = arrivalTime
			}

			if location.Departure != nil {
				departureTime, _ := time.Parse("15:04", location.Departure.ET)

				updateMap[fmt.Sprintf("stops.%s.departuretime", stopID)] = departureTime
			}
		}

		// Create update
		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		operations = append(operations, updateModel)
	}

	if len(operations) > 0 {
		_, err := realtimeJourneysCollection.BulkWrite(context.TODO(), operations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
		}
	}
}
