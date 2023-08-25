package darwin

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type PushPortData struct {
	TrainStatuses []TrainStatus
}

func (p *PushPortData) UpdateRealtimeJourneys(queue *railutils.BatchProcessingQueue) {
	now := time.Now()
	datasource := &ctdf.DataSource{
		OriginalFormat: "DarwinPushPort",
		Provider:       "National-Rail",
		Dataset:        "DarwinPushPort",
		Identifier:     now.String(),
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	journeysCollection := database.GetCollection("journeys")

	for _, trainStatus := range p.TrainStatuses {
		realtimeJourneyID := fmt.Sprintf("GB:NATIONALRAIL:%s:%s", trainStatus.SSD, trainStatus.UID)
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
				PrimaryIdentifier:      realtimeJourneyID,
				ActivelyTracked:        false,
				TimeoutDurationMinutes: 90,
				CreationDateTime:       now,
				Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,

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
			updateMap["activelytracked"] = realtimeJourney.ActivelyTracked
			updateMap["timeoutdurationminutes"] = realtimeJourney.TimeoutDurationMinutes

			updateMap["reliability"] = realtimeJourney.Reliability

			updateMap["creationdatetime"] = realtimeJourney.CreationDateTime
			updateMap["datasource"] = realtimeJourney.DataSource

			updateMap["journey"] = realtimeJourney.Journey
		} else {
			updateMap["datasource.identifier"] = datasource.Identifier
		}

		for _, location := range trainStatus.Locations {
			stop := stopCache.Get("Tiploc", location.TPL)

			if stop == nil {
				log.Debug().Str("tiploc", location.TPL).Msg("Failed to find stop")
				continue
			}

			journeyStop := realtimeJourney.Stops[stop.PrimaryIdentifier]
			journeyStopUpdated := false

			if realtimeJourney.Stops[stop.PrimaryIdentifier] == nil {
				journeyStop = &ctdf.RealtimeJourneyStops{
					StopRef:  stop.PrimaryIdentifier,
					TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				}
			}

			if location.Arrival != nil {
				arrivalTime, err := location.Arrival.GetTiming()

				if err == nil {
					journeyStop.ArrivalTime = arrivalTime
					journeyStopUpdated = true
				}
			}

			if location.Departure != nil {
				departureTime, err := location.Departure.GetTiming()

				if err == nil {
					journeyStop.DepartureTime = departureTime
					journeyStopUpdated = true
				}
			}

			if location.Platform != nil && location.Platform.CISPLATSUP != "true" && location.Platform.PLATSUP != "true" {
				journeyStop.Platform = location.Platform.Name
			}

			if journeyStopUpdated {
				updateMap[fmt.Sprintf("stops.%s", stop.PrimaryIdentifier)] = journeyStop
			}
		}

		if trainStatus.LateReason != "" {
			updateMap["annotations.LateReasonID"] = trainStatus.LateReason
			updateMap["annotations.LateReasonText"] = railutils.LateReasons[trainStatus.LateReason]
		}

		// Create update
		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		queue.Add(updateModel)
	}
}
