package darwin

import (
	"context"
	"fmt"
	"time"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PushPortData struct {
	TrainStatuses     []TrainStatus
	Schedules         []Schedule
	FormationLoadings []FormationLoading
	StationMessages   []StationMessage
	TrainAlerts       []TrainAlert
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
	stopsCollection := database.GetCollection("stops")

	// Parse Train Statuses
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

				Journey:        journey,
				JourneyRunDate: journeyDate,

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}

			newRealtimeJourney = true
		}

		updateMap := bson.M{
			"modificationdatetime":             now,
			"otheridentifiers.nationalrailrid": trainStatus.RID,
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
			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("GB:RAIL:DELAY:%s:%s", trainStatus.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     time.Now(),
				ModificationDateTime: time.Now(),

				DataSource: &ctdf.DataSource{},

				AlertType: ctdf.ServiceAlertTypeJourneyDelayed,

				Text: railutils.LateReasons[trainStatus.LateReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", trainStatus.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})
		}

		// Create update
		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		queue.Add(updateModel)
	}

	// Schedules
	for _, schedule := range p.Schedules {
		if schedule.CancelReason != "" {
			realtimeJourneyID := fmt.Sprintf("GB:NATIONALRAIL:%s:%s", schedule.SSD, schedule.UID)
			searchQuery := bson.M{"primaryidentifier": realtimeJourneyID}

			var realtimeJourney *ctdf.RealtimeJourney

			realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

			newRealtimeJourney := false
			if realtimeJourney == nil {
				// Find the journey for this train
				var journey *ctdf.Journey
				cursor, _ := journeysCollection.Find(context.Background(), bson.M{"otheridentifiers.TrainUID": schedule.UID})

				journeyDate, _ := time.Parse("2006-01-02", schedule.SSD)

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
					log.Error().Str("uid", schedule.UID).Msg("Failed to find respective Journey for this train")
					continue
				}

				// Construct the base realtime journey
				realtimeJourney = &ctdf.RealtimeJourney{
					PrimaryIdentifier:      realtimeJourneyID,
					ActivelyTracked:        false,
					TimeoutDurationMinutes: 90,
					CreationDateTime:       now,
					Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,

					Cancelled: true,

					DataSource: datasource,

					Journey:        journey,
					JourneyRunDate: journeyDate,

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
				updateMap["journeyrundate"] = realtimeJourney.JourneyRunDate
			} else {
				updateMap["datasource.identifier"] = datasource.Identifier
			}

			updateMap["cancelled"] = true

			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("GB:RAILCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     time.Now(),
				ModificationDateTime: time.Now(),

				DataSource: &ctdf.DataSource{},

				AlertType: ctdf.ServiceAlertTypeJourneyCancelled,

				Text: railutils.CancelledReasons[schedule.CancelReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})

			// Create update
			bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(searchQuery)
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			queue.Add(updateModel)

			log.Info().
				Str("realtimejourneyid", realtimeJourneyID).
				Str("reason", schedule.CancelReason).
				Msg("Train cancelled")
		}
	}

	// Formation Loading
	for _, formationLoading := range p.FormationLoadings {
		searchQuery := bson.M{"otheridentifiers.nationalrailrid": formationLoading.RID}

		var realtimeJourney *ctdf.RealtimeJourney

		realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

		if realtimeJourney != nil {
			log.Info().
				Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).
				Msg("Updated loading")
			pretty.Println(formationLoading)
		}
	}

	// Station Messages
	for _, stationMessage := range p.StationMessages {
		serviceAlertID := fmt.Sprintf("GB:NATIONALRAILSTATIONMESSAGE:%s", stationMessage.ID)

		if len(stationMessage.Stations) == 0 {
			log.Info().Str("servicealert", serviceAlertID).Msg("Removing Station Message Service Alert")

			deleteServiceAlert(serviceAlertID)
		} else {
			var alertType ctdf.ServiceAlertType
			switch stationMessage.Severity {
			case "0", "1":
				alertType = ctdf.ServiceAlertTypeInformation
			case "2", "3":
				alertType = ctdf.ServiceAlertTypeWarning
			default:
				alertType = ctdf.ServiceAlertTypeInformation
			}

			var matchedIdentifiers []string

			for _, station := range stationMessage.Stations {
				var stop *ctdf.Stop
				stopsCollection.FindOne(context.Background(), bson.M{"otheridentifiers.Crs": station.CRS}).Decode(&stop)

				if stop != nil {
					matchedIdentifiers = append(matchedIdentifiers, stop.PrimaryIdentifier)
				}
			}

			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    serviceAlertID,
				CreationDateTime:     time.Now(),
				ModificationDateTime: time.Now(),

				DataSource: &ctdf.DataSource{
					Provider: "National Rail",
				},

				AlertType: alertType,

				Text: stationMessage.Message.InnerXML,

				MatchedIdentifiers: matchedIdentifiers,

				ValidFrom:  time.Now(),
				ValidUntil: time.Now().Add(90 * 24 * time.Hour),
			})

			log.Info().Str("servicealert", serviceAlertID).Msg("Creating Station Message Service Alert")
		}
	}

	// Train Alert
	for _, trainAlert := range p.TrainAlerts {
		pretty.Println(trainAlert)
	}
}

func createServiceAlert(serviceAlert ctdf.ServiceAlert) {
	serviceAlertCollection := database.GetCollection("service_alerts")

	filter := bson.M{"primaryidentifier": serviceAlert.PrimaryIdentifier}
	update := bson.M{"$set": serviceAlert}
	opts := options.Update().SetUpsert(true)
	serviceAlertCollection.UpdateOne(context.TODO(), filter, update, opts)
}

func deleteServiceAlert(id string) {
	serviceAlertCollection := database.GetCollection("service_alerts")
	filter := bson.M{"primaryidentifier": id}

	serviceAlertCollection.DeleteOne(context.Background(), filter)
}
