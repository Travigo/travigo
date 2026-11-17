package darwin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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

	// GeneratedAt is the feed timestamp from the Pport element when present.
	GeneratedAt time.Time
}

// UpdateRealtimeJourneys submits each item in the frame for bounded,
// per-train-run ordered processing. Different runs are processed concurrently;
// schedule revisions and predictions for the same run remain in feed order.
func (p *PushPortData) UpdateRealtimeJourneys(dispatcher workDispatcher) {
	now := time.Now()
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "DarwinPushPort",
		ProviderName:   "National Rail",
		ProviderID:     "gb-nationalrail",
		DatasetID:      "gb-nationalrail-darwinpush",
		Timestamp:      now.String(),
	}

	if dispatcher == nil {
		dispatcher = inlineDispatcher{}
	}

	observeDarwinFeedLag(p.GeneratedAt, now, len(p.Schedules), len(p.TrainStatuses))

	// Schedules
	for _, schedule := range p.Schedules {
		schedule := schedule
		dispatcher.Submit(darwinRunKey(schedule.SSD, schedule.UID, schedule.RID), func() {
			source := *datasource
			updateDarwinSchedule(now, &source, schedule)
		})
	}

	// Parse Train Statuses
	// Schedule and status updates use optimistic merge saves because they can
	// overlap TRUST movement updates in another process.
	for _, trainStatus := range p.TrainStatuses {
		trainStatus := trainStatus
		dispatcher.Submit(darwinRunKey(trainStatus.SSD, trainStatus.UID, trainStatus.RID), func() {
			source := *datasource
			updateDarwinTrainStatus(now, &source, trainStatus)
		})
	}

	// Station Messages. Keying by message ID preserves update/removal order for
	// the same alert without serialising unrelated stations.
	for _, stationMessage := range p.StationMessages {
		stationMessage := stationMessage
		dispatcher.Submit("station:"+stationMessage.ID, func() {
			source := *datasource
			updateDarwinStationMessage(now, &source, stationMessage)
		})
	}

	// Train Alert
	for _, trainAlert := range p.TrainAlerts {
		trainAlert := trainAlert
		dispatcher.Submit("train-alert:"+trainAlert.AlertID, func() {
			storeDarwinTrainAlert(trainAlert)
		})
	}

	// Schedule formation
	for _, scheduleFormation := range p.ScheduleFormations {
		scheduleFormation := scheduleFormation
		dispatcher.Submit(darwinFormationKey(scheduleFormation.RID), func() {
			updateDarwinScheduleFormation(now, scheduleFormation)
		})
	}

	// Formation Loading. Formations are submitted before loadings and share the
	// same RID key, so allocation is written before occupancy for a run.
	for _, formationLoading := range p.FormationLoadings {
		formationLoading := formationLoading
		dispatcher.Submit(darwinFormationKey(formationLoading.RID), func() {
			updateDarwinFormationLoading(formationLoading)
		})
	}
}

func updateDarwinSchedule(now time.Time, datasource *ctdf.DataSourceReference, schedule Schedule) {
	realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", schedule.SSD, schedule.UID)
	snapshot, _ := realtimestore.FindForMergeUpdate(context.Background(), realtimeJourneyID)
	var realtimeJourney *ctdf.RealtimeJourney
	if snapshot != nil {
		realtimeJourney = snapshot.Journey
	}
	if realtimeJourney == nil && schedule.RID != "" {
		mapped, _ := realtimestore.FindByMapping(context.Background(), "nationalrailrid", schedule.RID)
		if mapped != nil {
			snapshot, _ = realtimestore.FindForMergeUpdate(context.Background(), mapped.PrimaryIdentifier)
			if snapshot != nil {
				realtimeJourney = snapshot.Journey
			}
		}
	}

	realtimeJourney, err := reconcileDarwinRun(context.Background(), realtimeJourney, realtimeJourneyID, schedule.UID, schedule.SSD, now, datasource)
	if err != nil {
		log.Error().Err(err).Str("uid", schedule.UID).Msg("Failed to resolve Darwin journey")
		return
	}
	if realtimeJourney == nil {
		return
	}
	if snapshot == nil {
		return
	}

	realtimeJourney.ModificationDateTime = now
	if realtimeJourney.OtherIdentifiers == nil {
		realtimeJourney.OtherIdentifiers = map[string]string{}
	}
	if schedule.RID != "" {
		realtimeJourney.OtherIdentifiers["nationalrailrid"] = schedule.RID
	}

	cancelCount := 0
	scheduleCancellations := map[string]darwinScheduleCancellation{}
	passengerCalls := schedule.passengerCalls()
	var calls []darwinCall

	for journeyStopIndex, scheduleStop := range passengerCalls {
		stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", scheduleStop.Tiploc))

		if stop == nil {
			log.Error().Str("tiploc", scheduleStop.Tiploc).Msg("Failed to find stop for schedule update")
			continue
		}

		calls = append(calls, darwinCall{
			stop: stop, arrival: scheduleClock(scheduleStop.PublicArrival, scheduleStop.WorkingArrival),
			departure: scheduleClock(scheduleStop.PublicDeparture, scheduleStop.WorkingDeparture),
			activity:  darwinCallActivity(scheduleStop.Activity), platform: scheduleStop.Platform,
		})
		scheduleCancellations[ctdf.RealtimeJourneyStopKey(stop.PrimaryIdentifier, journeyStopIndex)] = darwinScheduleCancellation{stopRef: stop.PrimaryIdentifier, journeyStopIndex: journeyStopIndex, cancelled: scheduleStop.Cancelled == "true"}
		if scheduleStop.Cancelled == "true" {
			cancelCount += 1
		}
	}

	if len(calls) == 0 {
		log.Error().
			Str("realtimejourneyid", realtimeJourneyID).
			Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
			Str("rid", schedule.RID).
			Msg("Skipping Darwin schedule update because no schedule stops resolved")
		return
	}

	if len(calls) == len(passengerCalls) {
		journeyCalls := buildDarwinCalls(calls)
		if len(journeyCalls) > 0 && darwinPathChanged(realtimeJourney.Journey, journeyCalls) {
			if err := publishDarwinPath(context.Background(), realtimeJourney, journeyCalls, schedule.SSD); err != nil {
				log.Error().Err(err).Str("journey", realtimeJourneyID).Msg("Failed to publish Darwin route amendment")
				return
			}
		}
	}

	log.Debug().Str("realtimejourneyid", realtimeJourneyID).Int("resolved_calls", len(calls)).Int("cancelled_calls", cancelCount).Msg("Darwin schedule cancellation summary")
	applyDarwinScheduleCancellationState(realtimeJourney, scheduleCancellations)

	if cancelCount > 0 && cancelCount == len(calls) {
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
	snapshot.Journey = realtimeJourney
	if err := realtimestore.SaveRealtimeJourneyMerged(context.Background(), snapshot); err != nil {
		log.Error().Err(err).Str("journey", realtimeJourneyID).Msg("Failed to save Darwin schedule realtime state")
		return
	}

	log.Info().
		Str("realtimejourneyid", realtimeJourneyID).
		Str("journeyid", realtimeJourney.Journey.PrimaryIdentifier).
		Msg("Train schedule updated")
}

func updateDarwinTrainStatus(now time.Time, datasource *ctdf.DataSourceReference, trainStatus TrainStatus) {
	realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", trainStatus.SSD, trainStatus.UID)
	snapshot, _ := realtimestore.FindForMergeUpdate(context.Background(), realtimeJourneyID)
	var realtimeJourney *ctdf.RealtimeJourney
	if snapshot != nil {
		realtimeJourney = snapshot.Journey
	}

	realtimeJourney, err := reconcileDarwinRun(context.Background(), realtimeJourney, realtimeJourneyID, trainStatus.UID, trainStatus.SSD, now, datasource)
	if err != nil {
		log.Error().Err(err).Str("uid", trainStatus.UID).Msg("Failed to resolve Darwin journey")
		return
	}
	if realtimeJourney == nil {
		return
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
	stopOccurrences := map[string]int{}
	for _, location := range trainStatus.Locations {
		stop := stopCache.Get(fmt.Sprintf("gb-tiploc-%s", location.TPL))

		if stop == nil {
			log.Debug().Str("tiploc", location.TPL).Msg("Failed to find stop")
			continue
		}

		journeyStopIndex := darwinJourneyStopOccurrenceIndex(realtimeJourney.Journey, stop, stopOccurrences[stop.PrimaryIdentifier])
		if journeyStopIndex < 0 {
			continue
		}
		stopOccurrences[stop.PrimaryIdentifier]++
		journeyStop := realtimeJourney.RealtimeStop(stop.PrimaryIdentifier, journeyStopIndex)
		journeyStopUpdated := false

		if journeyStop == nil {
			journeyStop = &ctdf.RealtimeJourneyStops{
				StopRef:          stop.PrimaryIdentifier,
				JourneyStopIndex: journeyStopIndex,
				TimeType:         ctdf.RealtimeJourneyStopTimeEstimatedFuture,
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
			realtimeJourney.SetRealtimeStop(journeyStop)
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

	if snapshot == nil {
		return
	}
	snapshot.Journey = realtimeJourney
	if err := realtimestore.SaveRealtimeJourneyMerged(context.Background(), snapshot); err != nil {
		log.Error().Err(err).Str("journey", realtimeJourneyID).Msg("Failed to save Darwin train status")
	}
}

func updateDarwinStationMessage(now time.Time, datasource *ctdf.DataSourceReference, stationMessage StationMessage) {
	serviceAlertID := fmt.Sprintf("gb-railstationmessage-%s", stationMessage.ID)

	if len(stationMessage.Stations) == 0 {
		log.Info().Str("servicealert", serviceAlertID).Msg("Removing Station Message Service Alert")

		railutils.DeleteServiceAlert(serviceAlertID)
		return
	}

	var alertType ctdf.ServiceAlertType
	switch stationMessage.Severity {
	case "0", "1":
		alertType = ctdf.ServiceAlertTypeInformation
	case "2", "3":
		alertType = ctdf.ServiceAlertTypeWarning
	default:
		alertType = ctdf.ServiceAlertTypeInformation
	}

	stopsCollection := database.GetCollection("stops")
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

func storeDarwinTrainAlert(trainAlert TrainAlert) {
	collection := database.GetCollection("datadump")
	collection.InsertOne(context.Background(), bson.M{
		"type":             "trainalert",
		"creationdatetime": time.Now(),
		"document":         trainAlert,
	})
}

// errDarwinJourneyNotFound means the formation arrived before its schedule was
// reconciled. The caller decides whether to persist a retry record.
var errDarwinJourneyNotFound = errors.New("realtime journey not found for Darwin formation")

func updateDarwinScheduleFormation(now time.Time, scheduleFormation ScheduleFormations) {
	if len(scheduleFormation.Formations) == 0 {
		return
	}
	if scheduleFormation.RID == "" {
		log.Error().Msg("Skipping Darwin formation without RID")
		return
	}

	err := applyDarwinScheduleFormation(context.Background(), scheduleFormation)
	if err == nil {
		return
	}

	if errors.Is(err, errDarwinJourneyNotFound) {
		log.Error().
			Str("rid", scheduleFormation.RID).
			Msg("Unable to find realtime journey for formation")
	} else {
		log.Error().Err(err).Str("rid", scheduleFormation.RID).Msg("Failed to update Darwin formation")
	}

	// Any failed apply is retried: the frame has already been acknowledged, so
	// dropping it here would lose the allocation permanently.
	insertMap := bson.M{}
	insertMap["type"] = "realtimedarwin_formation"
	insertMap["creationdatetime"] = now
	insertMap["record"] = scheduleFormation

	retryRecordsCollection := database.GetCollection("retry_records")
	if _, insertErr := retryRecordsCollection.InsertOne(context.Background(), insertMap); insertErr != nil {
		log.Error().Err(insertErr).Str("rid", scheduleFormation.RID).Msg("Failed to store Darwin formation retry record")
	}
}

// applyDarwinScheduleFormation writes the allocation and returns an error when
// the journey is still unknown or the write failed. RetryRecords uses the
// result to decide whether the record has been durably consumed.
func applyDarwinScheduleFormation(ctx context.Context, scheduleFormation ScheduleFormations) error {
	realtimeJourney, err := realtimestore.FindByMapping(ctx, "nationalrailrid", scheduleFormation.RID)
	if err != nil {
		return fmt.Errorf("find realtime journey %s: %w", scheduleFormation.RID, err)
	}
	if realtimeJourney == nil {
		return errDarwinJourneyNotFound
	}

	realtimeJourney.DetailedRailInformation.Trains = buildDarwinRailTrains(scheduleFormation)

	if err := realtimestore.UpdateRailDetailedAllocation(ctx, realtimeJourney.PrimaryIdentifier, realtimeJourney.DetailedRailInformation); err != nil {
		return fmt.Errorf("update rail allocation %s: %w", realtimeJourney.PrimaryIdentifier, err)
	}

	log.Info().
		Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).
		Msg("Updated formation")

	return nil
}

func updateDarwinFormationLoading(formationLoading FormationLoading) {
	realtimeJourney, _ := realtimestore.FindByMapping(context.Background(), "nationalrailrid", formationLoading.RID)

	if realtimeJourney == nil {
		log.Error().
			Str("rid", formationLoading.RID).
			Str("fid", formationLoading.FID).
			Str("tpl", formationLoading.TPL).
			Msg("Unable to find realtime journey for occupancy")
		return
	}

	log.Info().
		Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).
		Msg("Updated occupancy")

	applyDarwinFormationLoading(realtimeJourney, formationLoading)
	realtimestore.UpdateRailDetailedLoading(context.Background(), realtimeJourney.PrimaryIdentifier, realtimeJourney.DetailedRailInformation)

	// Calculate the total train occupancy based on percentage of all carriages
	totalOccupancy := 0
	totalCapacity := 0

	for _, train := range realtimeJourney.DetailedRailInformation.Trains {
		for _, carriage := range train.Carriages {
			if carriage.Occupancy < 0 {
				continue
			}
			totalCapacity += 100
			totalOccupancy += carriage.Occupancy
		}
	}

	if totalCapacity > 0 {
		realtimeJourney.Occupancy = ctdf.RealtimeJourneyOccupancy{
			OccupancyAvailable:       true,
			ActualValues:             false,
			TotalPercentageOccupancy: int((float64(totalOccupancy) / float64(totalCapacity)) * 100),
		}
	}

	snapshot, err := realtimestore.FindForMergeUpdate(context.Background(), realtimeJourney.PrimaryIdentifier)
	if err != nil {
		log.Error().Err(err).Str("journey", realtimeJourney.PrimaryIdentifier).Msg("Failed to read Darwin formation loading state")
		return
	}
	if snapshot.Journey == nil {
		return
	}
	snapshot.Journey.Occupancy = realtimeJourney.Occupancy
	if err := realtimestore.SaveRealtimeJourneyMerged(context.Background(), snapshot); err != nil {
		log.Error().Err(err).Str("journey", realtimeJourney.PrimaryIdentifier).Msg("Failed to save Darwin formation loading")
	}
}

var lastDarwinLagWarning atomic.Int64

// observeDarwinFeedLag reports when the feed's own timestamp is old enough that
// processing is visibly behind realtime. Warnings are limited to one per 30s.
func observeDarwinFeedLag(generatedAt, now time.Time, schedules, statuses int) {
	if generatedAt.IsZero() {
		return
	}

	age := now.Sub(generatedAt)
	if age < time.Minute {
		return
	}

	lastWarning := lastDarwinLagWarning.Load()
	if lastWarning != 0 && now.Sub(time.Unix(0, lastWarning)) < 30*time.Second {
		return
	}
	if !lastDarwinLagWarning.CompareAndSwap(lastWarning, now.UnixNano()) {
		return
	}

	log.Warn().
		Dur("feed_age", age).
		Int("schedules", schedules).
		Int("statuses", statuses).
		Msg("Darwin Push Port frame is behind realtime")
}
