package nrod

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/cif"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type VSTPMessage struct {
	VSTP struct {
		MessageID string `json:"originMsgId"`
		Owner     string `json:"owner"`
		Timestamp string `json:"timestamp"`

		Schedule struct {
			ScheduleSegment []ScheduleSegment `json:"schedule_segment"`

			TransactionType string `json:"transaction_type"`
			TrainStatus     string `json:"train_status"`
			TrainUID        string `json:"CIF_train_uid"`
			STP             string `json:"CIF_stp_indicator"`
			Timetable       string `json:"applicable_timetable"`

			StartDate          string `json:"schedule_start_date"`
			EndDate            string `json:"schedule_end_date"`
			DayRuns            string `json:"schedule_days_runs"`
			BankHolidayRunning string `json:"CIF_bank_holiday_running"`
		} `json:"schedule"`
	} `json:"VSTPCIFMsgV1"`
}

func (v *VSTPMessage) Process(stompClient *StompClient) {
	switch v.VSTP.Schedule.TransactionType {
	case "Create":
		if len(v.VSTP.Schedule.DayRuns) != 7 || strings.Trim(v.VSTP.Schedule.DayRuns, "01") != "" {
			log.Warn().Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Ignoring VSTP schedule with invalid operating days")
			return
		}
		switch v.VSTP.Schedule.STP {
		case "N":
			v.processCreate(nil, nil)
		case "C":
			v.processCancellation()
		case "O":
			v.processOverlay()
		default:
			log.Warn().Str("stp", v.VSTP.Schedule.STP).Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Ignoring unsupported VSTP STP indicator")
		}
	case "Delete":
		v.processDelete()
	default:
		log.Error().Str("transaction", v.VSTP.Schedule.TransactionType).Msg("Unhandled VSTP transaction")
	}
}

func (v *VSTPMessage) processOverlay() {
	ctx := context.Background()
	start, err := time.Parse(ctdf.YearMonthDayFormat, v.VSTP.Schedule.StartDate)
	if err != nil {
		log.Error().Err(err).Msg("Invalid VSTP overlay start date")
		return
	}
	end, err := time.Parse(ctdf.YearMonthDayFormat, v.VSTP.Schedule.EndDate)
	if err != nil {
		log.Error().Err(err).Msg("Invalid VSTP overlay end date")
		return
	}
	cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{
		"datasource.datasetid":      "gb-nationalrail-timetable",
		"otheridentifiers.TrainUID": v.VSTP.Schedule.TrainUID,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to find journeys for VSTP overlay")
		return
	}
	defer cursor.Close(ctx)
	var originals []*ctdf.Journey
	if err := cursor.All(ctx, &originals); err != nil {
		log.Error().Err(err).Msg("Failed to read journeys for VSTP overlay")
		return
	}
	var replaced, affectedStops []string
	datesByJourney := make(map[string][]string)
	for _, journey := range originals {
		dates := vstpMatchingServiceDates(journey, start, end, v.VSTP.Schedule.DayRuns)
		if len(dates) > 0 {
			datesByJourney[journey.PrimaryIdentifier] = dates
			replaced = append(replaced, journey.PrimaryIdentifier)
			affectedStops = append(affectedStops, journey.GetStopRefs()...)
		}
	}
	if !v.processCreate(replaced, affectedStops) {
		return
	}
	// Keep replacement metadata for realtime consumers and subscriptions.
	// Boards also read the overlay directly, so Redis cannot delay visibility.
	now := time.Now()
	timeout := vstpRealtimeTimeoutMinutes(end, now)
	if timeout <= 0 {
		return
	}
	for _, journey := range originals {
		dates := datesByJourney[journey.PrimaryIdentifier]
		if len(dates) == 0 {
			continue
		}
		journey.GetService()
		run := &ctdf.RealtimeJourney{
			PrimaryIdentifier:          fmt.Sprintf("gb-nationalrail-vstp-overlay-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
			TimeoutDurationMinutes:     timeout,
			ActivelyTracked:            true,
			Journey:                    journey,
			Service:                    journey.Service,
			JourneyRunDate:             start,
			CreationDateTime:           now,
			ModificationDateTime:       now,
			Reliability:                ctdf.RealtimeJourneyReliabilityExternalProvided,
			SuppressFromDepartures:     true,
			SuppressFromDepartureDates: dates,
			ReplacedByJourneyRef:       v.vstpJourneyID(),
			DataSource:                 &ctdf.DataSourceReference{OriginalFormat: "JSON-CIF", ProviderName: "Network Rail", ProviderID: "gb-networkrail", DatasetID: railutils.VSTPDataset},
		}
		if err := realtimestore.SaveRealtimeJourney(ctx, run); err != nil {
			log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to save VSTP replacement realtime state")
		}
	}
}

func (v *VSTPMessage) vstpJourneyID() string {
	return fmt.Sprintf("gb-rail-vstp-%s:%s:%s:%s", v.VSTP.Schedule.TrainUID, v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, v.VSTP.Schedule.STP)
}

func (v *VSTPMessage) processCancellation() {
	now := time.Now()
	startDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.StartDate)
	if err != nil {
		log.Error().Err(err).Msg("invalid VSTP cancellation start date")
		return
	}
	endDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.EndDate)
	if err != nil {
		log.Error().Err(err).Msg("invalid VSTP cancellation end date")
		return
	}
	timeoutDurationMinutes := vstpRealtimeTimeoutMinutes(endDate, now)
	if timeoutDurationMinutes <= 0 {
		log.Warn().Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Ignoring expired VSTP cancellation")
		return
	}

	journeysCollection := database.GetCollection("journeys")
	cursor, err := journeysCollection.Find(context.Background(), bson.M{
		"datasource.datasetid":      "gb-nationalrail-timetable",
		"otheridentifiers.TrainUID": v.VSTP.Schedule.TrainUID,
	})
	if err != nil {
		log.Error().Err(err).Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Failed to find permanent journey for VSTP cancellation")
		return
	}
	defer cursor.Close(context.Background())

	cancelledJourneys := 0
	for cursor.Next(context.Background()) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			log.Error().Err(err).Msg("Failed to decode permanent journey for VSTP cancellation")
			continue
		}
		cancelledDates := vstpMatchingServiceDates(&journey, startDate, endDate, v.VSTP.Schedule.DayRuns)
		if len(cancelledDates) == 0 {
			continue
		}
		// A realtime journey describes one run. Keep the complete set of
		// cancelled runs in the existing day-instance service alert mechanism.
		runDate, err := time.Parse(ctdf.YearMonthDayFormat, cancelledDates[0])
		if err != nil {
			log.Error().Err(err).Msg("Invalid VSTP cancellation service date")
			continue
		}
		identifiers := make([]string, 0, len(cancelledDates))
		for _, date := range cancelledDates {
			identifiers = append(identifiers, fmt.Sprintf("DAYINSTANCEOF:%s:%s", date, journey.PrimaryIdentifier))
		}
		alert := ctdf.ServiceAlert{
			PrimaryIdentifier: fmt.Sprintf("gb-networkrail-vstpcancel-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
			CreationDateTime:  now, ModificationDateTime: now,
			DataSource: &ctdf.DataSourceReference{OriginalFormat: "JSON-CIF", ProviderName: "Network Rail", ProviderID: "gb-networkrail", DatasetID: "gb-networkrail-vstp"},
			AlertType:  ctdf.ServiceAlertTypeJourneyCancelled,
			Text:       "Journey cancelled by rail operator", MatchedIdentifiers: identifiers,
			ValidFrom: now.Add(-time.Second), ValidUntil: endDate.Add(48 * time.Hour),
		}
		_, err = database.GetCollection("service_alerts").UpdateOne(context.Background(), bson.M{"primaryidentifier": alert.PrimaryIdentifier}, bson.M{"$set": alert}, options.Update().SetUpsert(true))
		if err != nil {
			log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to save VSTP cancellation alert")
			continue
		}

		journey.GetService()
		realtimeJourney := &ctdf.RealtimeJourney{
			PrimaryIdentifier:      fmt.Sprintf("gb-nationalrail-vstp-cancellation-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
			TimeoutDurationMinutes: timeoutDurationMinutes,
			ActivelyTracked:        true,
			Journey:                &journey,
			JourneyRunDate:         runDate,
			CreationDateTime:       now,
			ModificationDateTime:   now,
			Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,
			Cancelled:              true,
			Service:                journey.Service,
			Stops:                  map[string]*ctdf.RealtimeJourneyStops{},
			DataSource: &ctdf.DataSourceReference{
				OriginalFormat: "JSON-CIF",
				ProviderName:   "Network Rail",
				ProviderID:     "gb-networkrail",
				DatasetID:      railutils.VSTPDataset,
				Timestamp:      fmt.Sprintf("%d", now.Unix()),
			},
		}
		if err := realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney); err != nil {
			log.Error().Err(err).Str("journey", journey.PrimaryIdentifier).Msg("Failed to save VSTP cancellation realtime journey")
			continue
		}
		cancelledJourneys++
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Failed while reading permanent journeys for VSTP cancellation")
	}

	log.Info().
		Str("trainuid", v.VSTP.Schedule.TrainUID).
		Int("cancelled_journeys", cancelledJourneys).
		Msg("Applied VSTP cancellation")
}

func vstpMatchingServiceDates(journey *ctdf.Journey, startDate, endDate time.Time, daysRun string) []string {
	matchingDates := make([]string, 0)
	if journey == nil || journey.Availability == nil {
		return matchingDates
	}
	for date := startDate; !date.After(endDate); date = date.AddDate(0, 0, 1) {
		weekdayIndex := convertWeekday(date)
		if weekdayIndex >= len(daysRun) || daysRun[weekdayIndex] != '1' {
			continue
		}
		if journey.Availability.MatchDate(date) {
			matchingDates = append(matchingDates, date.Format(ctdf.YearMonthDayFormat))
		}
	}
	return matchingDates
}

func vstpRealtimeTimeoutMinutes(endDate, now time.Time) int {
	expiresAt := endDate.Add(48 * time.Hour)
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return int(remaining/time.Minute) + 1
}

func (v *VSTPMessage) processCreate(replaced, affectedStops []string) bool {
	startDate, err := time.Parse(ctdf.YearMonthDayFormat, v.VSTP.Schedule.StartDate)
	if err != nil {
		log.Error().Err(err).Msg("Invalid VSTP start date")
		return false
	}
	endDate, err := time.Parse(ctdf.YearMonthDayFormat, v.VSTP.Schedule.EndDate)
	if err != nil {
		log.Error().Err(err).Msg("Invalid VSTP end date")
		return false
	}

	now := time.Now()
	journeyID := v.vstpJourneyID()
	var operations []mongo.WriteModel
	for _, scheduleSegment := range v.VSTP.Schedule.ScheduleSegment {
		if !cif.IsValidPassengerJourney(scheduleSegment.TrainCategory, scheduleSegment.ATOC) {
			continue
		}

		trainDefinitionSet := &cif.TrainDefinitionSet{
			BasicSchedule: cif.BasicSchedule{
				TransactionType:          v.VSTP.Schedule.TransactionType,
				TrainUID:                 v.VSTP.Schedule.TrainUID,
				DateRunsFrom:             startDate.Format("060102"),
				DateRunsTo:               endDate.Format("060102"),
				DaysRun:                  v.VSTP.Schedule.DayRuns,
				BankHolidayRunning:       v.VSTP.Schedule.BankHolidayRunning,
				TrainStatus:              v.VSTP.Schedule.TrainStatus,
				TrainCategory:            scheduleSegment.TrainCategory,
				TrainIdentity:            scheduleSegment.SignallingID,
				Headcode:                 scheduleSegment.Headcode,
				TrainServiceCode:         scheduleSegment.TrainServiceCode,
				PowerType:                scheduleSegment.PowerType,
				TimingLoad:               scheduleSegment.TimingLoad,
				Speed:                    scheduleSegment.Speed,
				OperatingCharacteristics: scheduleSegment.OperatingCharacteristics,
				SeatingClass:             "",
				Sleepers:                 scheduleSegment.Sleepers,
				Reservations:             scheduleSegment.Reservations,
				ConnectionIndicator:      scheduleSegment.ConnectionIndicator,
				CateringCode:             scheduleSegment.CateringCode,
				STPIndicator:             v.VSTP.Schedule.STP,
			},
			BasicScheduleExtraDetails: cif.BasicScheduleExtraDetails{
				ATOCCode: scheduleSegment.ATOC,
			},
			IntermediateLocations: []*cif.IntermediateLocation{},
			ChangesEnRoute:        []*cif.ChangesEnRoute{},
		}

		// Add locations
		for index, location := range scheduleSegment.ScheduleLocations {
			if index == 0 {
				trainDefinitionSet.OriginLocation = cif.OriginLocation{
					Location:               location.Location.Tiploc.ID,
					ScheduledDepartureTime: strings.TrimSpace(location.ScheduledDepartureTime),
					PublicDepartureTime:    strings.TrimSpace(location.PublicDepartureTime),
					Platform:               location.Platform,
					Activity:               location.Activity,
				}
			} else if index == len(scheduleSegment.ScheduleLocations)-1 {
				trainDefinitionSet.TerminatingLocation = cif.TerminatingLocation{
					Location:             location.Location.Tiploc.ID,
					ScheduledArrivalTime: strings.TrimSpace(location.ScheduledArrivalTime),
					PublicArrivalTime:    strings.TrimSpace(location.PublicArrivalTime),
					Platform:             location.Platform,
					Path:                 location.Path,
					Activity:             location.Activity,
				}
			} else {
				trainDefinitionSet.IntermediateLocations = append(trainDefinitionSet.IntermediateLocations, &cif.IntermediateLocation{
					Location:               location.Location.Tiploc.ID,
					ScheduledArrivalTime:   strings.TrimSpace(location.ScheduledArrivalTime),
					ScheduledDepartureTime: strings.TrimSpace(location.ScheduledDepartureTime),
					PublicArrivalTime:      strings.TrimSpace(location.PublicArrivalTime),
					PublicDepartureTime:    strings.TrimSpace(location.PublicDepartureTime),
					Platform:               location.Platform,
					Activity:               location.Activity,
				})
			}
		}

		// Convert this to ctdf
		// Performance Review [Set L] [PR117] [P1] (realtime): Concurrent VSTP frames call the CIF
		// converter, whose package-global stopTIPLOCCache/missingStopTIPLOCCache are
		// ordinary maps read and written without synchronization. Concurrent cold
		// resolutions can race or panic. Give conversion a shared immutable/versioned
		// stop snapshot or synchronize/coalesce cache fills before adding concurrency;
		// a new CommonInterfaceFormat value does not isolate those package globals.
		cifDoc := cif.CommonInterfaceFormat{}
		journey := cifDoc.CreateJourneyFromTraindef(journeyID, trainDefinitionSet)
		journey.DataSource = &ctdf.DataSourceReference{
			OriginalFormat: "JSON-CIF",
			ProviderName:   "Network Rail",
			ProviderID:     "gb-networkrail",
			DatasetID:      railutils.VSTPDataset,
			Timestamp:      fmt.Sprintf("%d", now.Unix()),
		}
		journey.Expiry = endDate.Add(48 * time.Hour)

		if len(journey.Path) == 0 {
			continue
		}
		journey.ReplacesJourneyRefs = replaced
		journey.AmendmentStopRefs = affectedStops

		operations = append(operations, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"primaryidentifier": journeyID}).
			SetReplacement(journey).
			SetUpsert(true))
	}
	if len(operations) == 0 {
		return false
	}

	ctx := context.Background()
	for _, name := range []string{database.JourneysRawCollectionName, database.JourneysCollectionName} {
		if _, err := database.GetCollection(name).BulkWrite(ctx, operations); err != nil {
			log.Error().Err(err).Str("collection", name).Msg("Failed to write VSTP journeys")
			return false
		}
	}

	railutils.CreateServiceAlert(ctdf.ServiceAlert{
		PrimaryIdentifier:    fmt.Sprintf("gb-networkrail-vstpcreate-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journeyID),
		CreationDateTime:     now,
		ModificationDateTime: now,
		DataSource: &ctdf.DataSourceReference{
			OriginalFormat: "JSON-CIF",
			ProviderName:   "Network Rail",
			ProviderID:     "gb-networkrail",
			DatasetID:      railutils.VSTPDataset,
			Timestamp:      fmt.Sprintf("%d", now.Unix()),
		},
		AlertType:          ctdf.ServiceAlertTypeInformation,
		Text:               "This timetable entry was added at short notice and is not part of a regularly scheduled service",
		MatchedIdentifiers: []string{journeyID},
		ValidFrom:          startDate,
		ValidUntil:         endDate.Add(24 * time.Hour),
	})
	log.Info().Str("journey", journeyID).Msg("Published VSTP journey")
	return true
}

func (v *VSTPMessage) processDelete() {
	ctx := context.Background()
	if v.VSTP.Schedule.STP == "C" {
		cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{
			"datasource.datasetid":      "gb-nationalrail-timetable",
			"otheridentifiers.TrainUID": v.VSTP.Schedule.TrainUID,
		})
		if err != nil {
			log.Error().Err(err).Msg("Failed to find cancelled VSTP journeys")
			return
		}
		defer cursor.Close(ctx)
		for cursor.Next(ctx) {
			var journey ctdf.Journey
			if err := cursor.Decode(&journey); err != nil {
				log.Error().Err(err).Msg("Failed to decode cancelled VSTP journey")
				continue
			}
			suffix := fmt.Sprintf("%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier)
			railutils.DeleteServiceAlert("gb-networkrail-vstpcancel-" + suffix)
			if err := realtimestore.DeleteRealtimeJourneyDetails(ctx, "gb-nationalrail-vstp-cancellation-"+suffix); err != nil {
				log.Error().Err(err).Msg("Failed to clear VSTP cancellation realtime state")
			}
		}
		if err := cursor.Err(); err != nil {
			log.Error().Err(err).Msg("Failed to read cancelled VSTP journeys")
		}
		return
	}

	id := v.vstpJourneyID()
	if v.VSTP.Schedule.STP == "O" {
		var overlay ctdf.Journey
		err := database.GetCollection("journeys").FindOne(ctx, bson.M{"primaryidentifier": id}).Decode(&overlay)
		if err != nil && err != mongo.ErrNoDocuments {
			log.Error().Err(err).Str("journey", id).Msg("Failed to load withdrawn VSTP overlay")
			return
		}
		for _, originalID := range overlay.ReplacesJourneyRefs {
			legacyID := fmt.Sprintf("gb-nationalrail-vstp-overlay-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, originalID)
			if err := realtimestore.DeleteRealtimeJourneyDetails(ctx, legacyID); err != nil {
				log.Error().Err(err).Str("journey", id).Msg("Failed to clear legacy VSTP overlay state")
				return
			}
		}
	}
	for _, collection := range []string{database.JourneysRawCollectionName, database.JourneysCollectionName} {
		if _, err := database.GetCollection(collection).DeleteMany(ctx, bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": id},
			bson.M{"datasource.datasetid": railutils.DarwinAmendmentDataset, "replacesjourneyrefs": id},
		}}); err != nil {
			log.Error().Err(err).Str("journey", id).Msg("Failed to delete VSTP journey")
			return
		}
	}
	railutils.DeleteServiceAlert(fmt.Sprintf("gb-networkrail-vstpcreate-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, id))
}

type ScheduleSegment struct {
	ScheduleLocations []ScheduleLocation `json:"schedule_location"`

	SignallingID             string `json:"signalling_id"`
	ATOC                     string `json:"atoc_code"`
	TrainServiceCode         string `json:"CIF_train_service_code"`
	TrainClass               string `json:"CIF_train_class"`
	TrainCategory            string `json:"CIF_train_category"`
	Speed                    string `json:"CIF_speed"`
	PowerType                string `json:"CIF_power_type"`
	Headcode                 string `json:"CIF_headcode"`
	CourseIndicator          string `json:"CIF_course_indicator"`
	OperatingCharacteristics string `json:"CIF_operating_characteristics"`
	Sleepers                 string `json:"CIF_sleepers"`
	Reservations             string `json:"CIF_reservations"`
	ConnectionIndicator      string `json:"CIF_connection_indicator"`
	CateringCode             string `json:"CIF_catering_code"`
	ServiceBranding          string `json:"CIF_service_branding"`
	TractionClass            string `json:"CIF_traction_class"`
	TimingLoad               string `json:"CIF_timing_load"`
}

type ScheduleLocation struct {
	Location               ScheduleLocationLocation `json:"location"`
	ScheduledPassTime      string                   `json:"scheduled_pass_time"`
	ScheduledDepartureTime string                   `json:"scheduled_departure_time"`
	ScheduledArrivalTime   string                   `json:"scheduled_arrival_time"`
	PublicDepartureTime    string                   `json:"public_departure_time"`
	PublicArrivalTime      string                   `json:"public_arrival_time"`
	Path                   string                   `json:"CIF_path"`
	Activity               string                   `json:"CIF_activity"`
	Platform               string                   `json:"CIF_platform"`
	Line                   string                   `json:"CIF_line"`
}

type ScheduleLocationLocation struct {
	Tiploc struct {
		ID string `json:"tiploc_id"`
	} `json:"tiploc"`
}

func convertWeekday(date time.Time) int {
	convertedInt := int(date.Weekday()) - 1

	if convertedInt == -1 {
		return 6
	}

	return convertedInt
}
