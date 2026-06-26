package darwin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

type PushPortData struct {
	TrainStatuses      []TrainStatus
	Schedules          []Schedule
	FormationLoadings  []FormationLoading
	StationMessages    []StationMessage
	TrainAlerts        []TrainAlert
	ScheduleFormations []ScheduleFormations
}

func (p *PushPortData) UpdateRealtimeJourneys() {
	now := time.Now()
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "DarwinPushPort",
		ProviderName:   "National Rail",
		ProviderID:     "gb-nationalrail",
		DatasetID:      "gb-nationalrail-darwinpush",
		Timestamp:      now.String(),
	}

	journeysCollection := database.GetCollection("journeys")
	stopsCollection := database.GetCollection("stops")
	retryRecordsCollection := database.GetCollection("retry_records")

	// Parse Train Statuses
	for _, trainStatus := range p.TrainStatuses {
		realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", trainStatus.SSD, trainStatus.UID)
		realtimeJourney, _ := realtimestore.FindByIdentifier(context.Background(), realtimeJourneyID)

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
		}

		realtimeJourney.ModificationDateTime = now

		if realtimeJourney.OtherIdentifiers == nil {
			realtimeJourney.OtherIdentifiers = map[string]string{}
		}
		realtimeJourney.OtherIdentifiers["nationalrailrid"] = trainStatus.RID

		// Update database
		if realtimeJourney.DataSource == nil {
			realtimeJourney.DataSource = datasource
		}
		realtimeJourney.DataSource.Timestamp = datasource.Timestamp
		realtimeJourney.ActivelyTracked = true

		if realtimeJourney.Stops == nil {
			realtimeJourney.Stops = map[string]*ctdf.RealtimeJourneyStops{}
		}
		if realtimeJourney.Journey == nil {
			log.Error().Str("realtimejourney", realtimeJourney.PrimaryIdentifier).Msg("Realtime Journey missing Journey")
			continue
		}

		for _, location := range trainStatus.Locations {
			stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", location.TPL))

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
			journeyStop.StopRef = stop.PrimaryIdentifier

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
				journeyStopUpdated = true
			}

			if journeyStopUpdated {
				realtimeJourney.Stops[stop.PrimaryIdentifier] = journeyStop
			}
		}

		if trainStatus.LateReason != "" && realtimeJourney.Journey != nil {
			railutils.CreateServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("gb-raildelay-:%s:%s", trainStatus.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: datasource,

				AlertType: ctdf.ServiceAlertTypeJourneyDelayed,

				Text: railutils.LateReasons[trainStatus.LateReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", trainStatus.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})
		}

		realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)
	}

	// Schedules
	for _, schedule := range p.Schedules {
		realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", schedule.SSD, schedule.UID)
		realtimeJourney, _ := realtimestore.FindByIdentifier(context.Background(), realtimeJourneyID)
		if realtimeJourney == nil && schedule.RID != "" {
			realtimeJourney, _ = realtimestore.FindByMapping(context.Background(), "nationalrailrid", schedule.RID)
		}

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
		}

		if realtimeJourney.Journey == nil {
			log.Error().Str("id", realtimeJourney.PrimaryIdentifier).Msg("Realtime journey without journey - this should never happen :(")
			continue
		}

		realtimeJourney.ModificationDateTime = now
		if realtimeJourney.OtherIdentifiers == nil {
			realtimeJourney.OtherIdentifiers = map[string]string{}
		}
		if schedule.RID != "" {
			realtimeJourney.OtherIdentifiers["nationalrailrid"] = schedule.RID
		}

		// Calculate the new stops
		scheduleStops := []ScheduleStop{
			schedule.Origin,
		}
		scheduleStops = append(scheduleStops, schedule.Intermediate...)
		scheduleStops = append(scheduleStops, schedule.Destination)

		cancelCount := 0
		resolvedScheduleStopCount := 0
		scheduleCancellations := map[string]bool{}

		for scheduleStopIndex, scheduleStop := range scheduleStops {
			stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", scheduleStop.Tiploc))

			if stop == nil {
				log.Error().Str("tiploc", scheduleStop.Tiploc).Msg("Failed to find stop for schedule update")
				continue
			}

			log.Info().
				Str("realtimejourneyid", realtimeJourneyID).
				Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
				Str("rid", schedule.RID).
				Str("uid", schedule.UID).
				Str("ssd", schedule.SSD).
				Int("schedule_stop_index", scheduleStopIndex).
				Str("tiploc", scheduleStop.Tiploc).
				Str("stop_ref", stop.PrimaryIdentifier).
				Str("stop_name", stop.PrimaryName).
				Str("pta", scheduleStop.PublicArrival).
				Str("ptd", scheduleStop.PublicDeparture).
				Str("wta", scheduleStop.WorkingArrival).
				Str("wtd", scheduleStop.WorkingDeparture).
				Str("cancelled", scheduleStop.Cancelled).
				Bool("existing_cancelled", realtimeJourney.Stops[stop.PrimaryIdentifier] != nil && realtimeJourney.Stops[stop.PrimaryIdentifier].Cancelled).
				Msg("Darwin schedule stop cancellation state")

			resolvedScheduleStopCount += 1
			scheduleCancellations[stop.PrimaryIdentifier] = scheduleStop.Cancelled == "true"
			if scheduleStop.Cancelled == "true" {
				cancelCount += 1
			}
		}

		if resolvedScheduleStopCount == 0 {
			log.Error().
				Str("realtimejourneyid", realtimeJourneyID).
				Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
				Str("rid", schedule.RID).
				Msg("Skipping Darwin schedule update because no schedule stops resolved")
			continue
		}

		applyDarwinScheduleCancellationState(realtimeJourney, scheduleCancellations)

		if cancelCount > 0 && cancelCount == resolvedScheduleStopCount {
			railutils.CreateServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("gb-railcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: datasource,

				AlertType: ctdf.ServiceAlertTypeJourneyCancelled,

				Text: railutils.CancelledReasons[schedule.CancelReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})
			railutils.DeleteServiceAlert(fmt.Sprintf("gb-railpartialcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))

			realtimeJourney.Cancelled = true

			log.Info().
				Str("realtimejourneyid", realtimeJourneyID).
				Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
				Msg("Train cancelled")
		} else if cancelCount > 0 {
			railutils.CreateServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("gb-railpartialcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: datasource,

				AlertType: ctdf.ServiceAlertTypeJourneyPartiallyCancelled,

				Text: railutils.CancelledReasons[schedule.CancelReason],

				MatchedIdentifiers: []string{fmt.Sprintf("DAYINSTANCEOF:%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier)},

				ValidFrom:  realtimeJourney.JourneyRunDate,
				ValidUntil: realtimeJourney.JourneyRunDate.Add(48 * time.Hour),
			})

			railutils.DeleteServiceAlert(fmt.Sprintf("gb-railcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))

			realtimeJourney.Cancelled = false
		} else {
			realtimeJourney.Cancelled = false

			railutils.DeleteServiceAlert(fmt.Sprintf("gb-railcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))
			railutils.DeleteServiceAlert(fmt.Sprintf("gb-railpartialcancel-%s:%s", schedule.SSD, realtimeJourney.Journey.PrimaryIdentifier))
		}

		// Update database
		if realtimeJourney.DataSource == nil {
			realtimeJourney.DataSource = datasource
		}
		realtimeJourney.DataSource.Timestamp = datasource.Timestamp
		realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)

		log.Info().
			Str("realtimejourneyid", realtimeJourneyID).
			Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
			Msg("Train schedule updated")
	}

	// Station Messages
	for _, stationMessage := range p.StationMessages {
		serviceAlertID := fmt.Sprintf("gb-railstationmessage-%s", stationMessage.ID)

		if len(stationMessage.Stations) == 0 {
			log.Info().Str("servicealert", serviceAlertID).Msg("Removing Station Message Service Alert")

			railutils.DeleteServiceAlert(serviceAlertID)
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

			railutils.CreateServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    serviceAlertID,
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: datasource,

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
		collection := database.GetCollection("datadump")
		collection.InsertOne(context.Background(), bson.M{
			"type":             "trainalert",
			"creationdatetime": time.Now(),
			"document":         trainAlert,
		})
	}

	// Schedule formation
	for _, scheduleFormation := range p.ScheduleFormations {
		if len(scheduleFormation.Formations) == 0 {
			continue
		}

		realtimeJourney, _ := realtimestore.FindByMapping(context.Background(), "nationalrailrid", scheduleFormation.RID)

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

			realtimeJourney.DetailedRailInformation.Carriages = buildDarwinRailCarriages(scheduleFormation)

			realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)
		}
	}

	// Formation Loading
	for _, formationLoading := range p.FormationLoadings {
		realtimeJourney, _ := realtimestore.FindByMapping(context.Background(), "nationalrailrid", formationLoading.RID)

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

			applyDarwinFormationLoading(realtimeJourney, formationLoading)

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

			realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)
		}
	}
}
