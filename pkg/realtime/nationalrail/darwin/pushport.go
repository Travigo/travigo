package darwin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	TrainStatuses      []TrainStatus
	Schedules          []Schedule
	FormationLoadings  []FormationLoading
	StationMessages    []StationMessage
	TrainAlerts        []TrainAlert
	ScheduleFormations []ScheduleFormations
}

func (p *PushPortData) UpdateRealtimeJourneys(queue *railutils.BatchProcessingQueue) {
	now := time.Now()
	datasource := &ctdf.DataSource{
		OriginalFormat: "DarwinPushPort",
		Provider:       "National-Rail",
		DatasetID:      "gb-nationalrail-darwinpush",
		Timestamp:      now.String(),
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	journeysCollection := database.GetCollection("journeys")
	stopsCollection := database.GetCollection("stops")
	retryRecordsCollection := database.GetCollection("retry_records")

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

			for cursor.Next(context.Background()) {
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
				log.Debug().Str("uid", trainStatus.UID).Msg("Failed to find respective Journey for this train status update")
				continue
			}

			journey.GetService()

			// Construct the base realtime journey
			realtimeJourney = &ctdf.RealtimeJourney{
				PrimaryIdentifier:      realtimeJourneyID,
				ActivelyTracked:        true,
				TimeoutDurationMinutes: 181,
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

			updateMap["service"] = realtimeJourney.Service
		} else {
			updateMap["datasource.timestamp"] = datasource.Timestamp
			updateMap["activelytracked"] = true
		}

		for _, location := range trainStatus.Locations {
			stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", location.TPL))

			if stop == nil {
				log.Debug().Str("tiploc", location.TPL).Msg("Failed to find stop")
				continue
			}

			if realtimeJourney.Journey == nil {
				log.Error().Str("realtimejourney", realtimeJourney.PrimaryIdentifier).Msg("Realtime Journey missing Journey")
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

		if trainStatus.LateReason != "" && realtimeJourney.Journey != nil {
			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("GB:RAIL:DELAY:%s:%s", trainStatus.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

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

			for cursor.Next(context.Background()) {
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
				log.Debug().Str("uid", schedule.UID).Msg("Failed to find respective Journey for this train schedule update")
				continue
			}

			journey.GetService()

			// Construct the base realtime journey
			realtimeJourney = &ctdf.RealtimeJourney{
				PrimaryIdentifier:      realtimeJourneyID,
				ActivelyTracked:        false,
				TimeoutDurationMinutes: 181,
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

		if realtimeJourney.Journey == nil {
			log.Error().Str("id", realtimeJourney.PrimaryIdentifier).Msg("Realtime journey without journey - this should never happen :(")
			continue
		}

		updateMap := bson.M{
			"modificationdatetime": now,
		}

		// Calculate the new stops
		scheduleStops := []ScheduleStop{
			schedule.Origin,
		}
		scheduleStops = append(scheduleStops, schedule.Intermediate...)
		scheduleStops = append(scheduleStops, schedule.Destination)

		cancelCount := 0

		for _, scheduleStop := range scheduleStops {
			stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", scheduleStop.Tiploc))

			if stop == nil {
				log.Error().Str("tiploc", scheduleStop.Tiploc).Msg("Failed to find stop for schedule update")
				continue
			}

			if scheduleStop.Cancelled == "true" {
				updateMap[fmt.Sprintf("stops.%s.cancelled", stop.PrimaryIdentifier)] = true
				pretty.Println(realtimeJourneyID, stop.PrimaryIdentifier)

				cancelCount += 1
			} else {
				updateMap[fmt.Sprintf("stops.%s.cancelled", stop.PrimaryIdentifier)] = false
			}
		}

		if cancelCount > 0 && cancelCount == len(scheduleStops) {
			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("GB:RAILCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: &ctdf.DataSource{},

				AlertType: ctdf.ServiceAlertTypeJourneyCancelled,

				Text: railutils.CancelledReasons[schedule.CancelReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})
			deleteServiceAlert(fmt.Sprintf("GB:RAILPARTIALCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))

			updateMap["cancelled"] = true

			log.Info().
				Str("realtimejourneyid", realtimeJourneyID).
				Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
				Msg("Train cancelled")
		} else if cancelCount > 0 {
			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("GB:RAILPARTIALCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: &ctdf.DataSource{},

				AlertType: ctdf.ServiceAlertTypeJourneyPartiallyCancelled,

				Text: railutils.CancelledReasons[schedule.CancelReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})

			deleteServiceAlert(fmt.Sprintf("GB:RAILCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))

			pretty.Println("partialcancel", realtimeJourney.PrimaryIdentifier, schedule.InnerXML)
		} else {
			updateMap["cancelled"] = false

			deleteServiceAlert(fmt.Sprintf("GB:RAILCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))
			deleteServiceAlert(fmt.Sprintf("GB:RAILPARTIALCANCEL:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))
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

		queue.Add(updateModel)

		log.Info().
			Str("realtimejourneyid", realtimeJourneyID).
			Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
			Msg("Train schedule updated")
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
				stopsCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("gb-crs-%s", station.CRS)}).Decode(&stop)

				if stop != nil {
					matchedIdentifiers = append(matchedIdentifiers, stop.PrimaryIdentifier)
				}
			}

			alertText := strings.TrimSpace(stationMessage.Message.InnerXML)
			alertText = strings.ReplaceAll(alertText, "&amp;", "&")
			alertText = strings.ReplaceAll(alertText, "<ns7:p>", "<p>")
			alertText = strings.ReplaceAll(alertText, "</ns7:p>", "</p>")
			alertText = strings.ReplaceAll(alertText, "<ns7:a href=", "<a href=")
			alertText = strings.ReplaceAll(alertText, "</ns7:a>", "</a>")

			createServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    serviceAlertID,
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: &ctdf.DataSource{
					Provider: "National Rail",
				},

				AlertType: alertType,

				Text: alertText,

				MatchedIdentifiers: matchedIdentifiers,

				ValidFrom:  now,
				ValidUntil: now.Add(5 * 24 * time.Hour),
			})

			log.Info().Str("servicealert", serviceAlertID).Msg("Creating Station Message Service Alert")
		}
	}

	// Train Alert
	for _, trainAlert := range p.TrainAlerts {
		pretty.Println(trainAlert)
	}

	// Schedule formation
	for _, scheduleFormation := range p.ScheduleFormations {
		if len(scheduleFormation.Formations) == 0 {
			continue
		}

		searchQuery := bson.M{"otheridentifiers.nationalrailrid": scheduleFormation.RID}

		var realtimeJourney *ctdf.RealtimeJourney

		realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

		if realtimeJourney == nil {
			log.Error().
				Str("rid", scheduleFormation.RID).
				Msg("Unable to find realtime journey for formation")

			insertMap := bson.M{}
			insertMap["type"] = "realtimedarwin_formation"
			insertMap["creationdatetime"] = now
			insertMap["record"] = scheduleFormation

			retryRecordsCollection.InsertOne(context.Background(), insertMap)
		} else {
			log.Info().
				Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).
				Msg("Updated formation")

			var realtimeCarriages []ctdf.RailCarriage

			// TODO: only handing the 1 formation here :(
			for _, carriage := range scheduleFormation.Formations[0].Coaches {
				var toilets []ctdf.RailCarriageToilet

				for _, toilet := range carriage.Toilets {
					toilets = append(toilets, ctdf.RailCarriageToilet{
						Type:   toilet.Type,
						Status: toilet.Status,
					})
				}
				realtimeCarriages = append(realtimeCarriages, ctdf.RailCarriage{
					ID:        carriage.Number,
					Class:     carriage.Class,
					Toilets:   toilets,
					Occupancy: -1,
				})
			}
			realtimeJourney.DetailedRailInformation.Carriages = realtimeCarriages

			updateMap := bson.M{}
			updateMap["detailedrailinformation"] = realtimeJourney.DetailedRailInformation

			bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(searchQuery)
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			queue.Add(updateModel)
		}
	}

	// Formation Loading
	for _, formationLoading := range p.FormationLoadings {
		searchQuery := bson.M{"otheridentifiers.nationalrailrid": formationLoading.RID}

		var realtimeJourney *ctdf.RealtimeJourney

		realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

		if realtimeJourney == nil {
			log.Error().
				Str("rid", formationLoading.RID).
				Str("fid", formationLoading.FID).
				Str("tpl", formationLoading.TPL).
				Msg("Unable to find realtime journey for occupancy")
		} else {
			log.Info().
				Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).
				Msg("Updated occupancy")

			for _, loading := range formationLoading.Loading {
				occupancy, _ := strconv.Atoi(loading.LoadingPercentage)
				carriageFound := false

				for _, carriage := range realtimeJourney.DetailedRailInformation.Carriages {
					if carriage.ID == loading.CoachNumber {
						carriage.Occupancy = occupancy
						carriageFound = true
						break
					}
				}

				if !carriageFound {
					realtimeJourney.DetailedRailInformation.Carriages = append(realtimeJourney.DetailedRailInformation.Carriages, ctdf.RailCarriage{
						ID:        loading.CoachNumber,
						Occupancy: occupancy,
					})
				}
			}

			// Calculate the total train occupancy based on percentage of all carriages
			totalOccupancy := 0
			totalCapacity := 0

			for _, carriage := range realtimeJourney.DetailedRailInformation.Carriages {
				totalCapacity += 100
				totalOccupancy += carriage.Occupancy
			}

			realtimeJourney.Occupancy = ctdf.RealtimeJourneyOccupancy{
				OccupancyAvailable:       true,
				ActualValues:             false,
				TotalPercentageOccupancy: int((float64(totalOccupancy) / float64(totalCapacity)) * 100),
			}

			updateMap := bson.M{}
			updateMap["occupancy"] = realtimeJourney.Occupancy
			updateMap["detailedrailinformation"] = realtimeJourney.DetailedRailInformation

			bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(searchQuery)
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			queue.Add(updateModel)
		}
	}
}

func createServiceAlert(serviceAlert ctdf.ServiceAlert) {
	serviceAlertCollection := database.GetCollection("service_alerts")

	filter := bson.M{"primaryidentifier": serviceAlert.PrimaryIdentifier}
	update := bson.M{"$set": serviceAlert}
	opts := options.Update().SetUpsert(true)
	serviceAlertCollection.UpdateOne(context.Background(), filter, update, opts)
}

func deleteServiceAlert(id string) {
	serviceAlertCollection := database.GetCollection("service_alerts")
	filter := bson.M{"primaryidentifier": id}

	serviceAlertCollection.DeleteOne(context.Background(), filter)
}
