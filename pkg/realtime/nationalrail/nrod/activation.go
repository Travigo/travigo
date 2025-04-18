package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type TrustActivation struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`

	ScheduleSource              string `json:"schedule_source"`
	TrainFileAddress            string `json:"train_file_address"`
	TrainUID                    string `json:"train_uid"`
	CreationTimestamp           string `json:"creation_timestamp"`
	TrainPlannedOriginTimestamp string `json:"tp_origin_timestamp"`
	TrainPlannedOriginStanox    string `json:"tp_origin_stanox"`
	OriginDepartureTimestamp    string `json:"origin_dep_timestamp"`
	TrainServiceCode            string `json:"train_service_code"`
	D1266RecordNumber           string `json:"d1266_record_number"`
	TrainCallType               string `json:"train_call_type"`
	TrainCallMode               string `json:"train_call_mode"`
	ScheduleType                string `json:"schedule_type"`
	ScheduleOriginStanox        string `json:"sched_origin_stanox"`
	ScheduleWorkingTimetableID  string `json:"schedule_wtt_id"`
	ScheduleStartDate           string `json:"schedule_start_date"`
	ScheduleEndDate             string `json:"schedule_end_date"`
}

func (a *TrustActivation) Process(stompClient *StompClient) {
	now := time.Now()
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "TrainActivationJSON",
		ProviderName:   "Network Rail",
		ProviderID:     "gb-networkrail",
		DatasetID:      "gb-networkrail-activation",
		Timestamp:      now.String(),
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	journeysCollection := database.GetCollection("journeys")

	realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", a.TrainPlannedOriginTimestamp, a.TrainUID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyID}

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

	newRealtimeJourney := false
	if realtimeJourney == nil {
		// Find the journey for this train
		var journey *ctdf.Journey
		cursor, _ := journeysCollection.Find(context.Background(), bson.M{"otheridentifiers.TrainUID": a.TrainUID})

		journeyDate, _ := time.Parse("2006-01-02", a.TrainPlannedOriginTimestamp)
		journeyPotentials := 0

		for cursor.Next(context.Background()) {
			var potentialJourney *ctdf.Journey
			err := cursor.Decode(&potentialJourney)
			if err != nil {
				log.Error().Err(err).Msg("Failed to decode Journey")
			}
			journeyPotentials += 1

			if potentialJourney.Availability.MatchDate(journeyDate) {
				journey = potentialJourney
			}
		}

		if journey == nil {
			log.Error().Str("uid", a.TrainUID).Str("toc", a.OperatorID).Int("journeypotentials", journeyPotentials).Msg("Failed to find respective Journey for this train")
			return
		}

		journey.GetService()

		// Construct the base realtime journey
		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier: realtimeJourneyID,
			OtherIdentifiers: map[string]string{
				"TrainID":  a.TrainID,
				"TrainUID": a.TrainUID,
			},
			TimeoutDurationMinutes: 181,
			ActivelyTracked:        false,
			CreationDateTime:       now,
			Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,

			DataSource: datasource,

			Journey:        journey,
			JourneyRunDate: journeyDate,
			Service:        journey.Service,

			Stops: map[string]*ctdf.RealtimeJourneyStops{},
		}

		newRealtimeJourney = true
	}

	updateMap := bson.M{
		"modificationdatetime":     now,
		"otheridentifiers.TrainID": a.TrainID,
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
		updateMap["journeyrundate"] = realtimeJourney.JourneyRunDate

		updateMap["service"] = realtimeJourney.Service
	} else {
		updateMap["datasource.timestamp"] = datasource.Timestamp
	}

	// Create update
	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(searchQuery)
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	stompClient.Queue.Add(updateModel)

	log.Info().
		Str("trainid", a.TrainID).
		Str("trainuid", a.TrainUID).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train activated")
}
