package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/cif"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
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
	if v.VSTP.Schedule.TransactionType == "Create" {
		v.processCreate()
	} else if v.VSTP.Schedule.TransactionType == "Delete" {
		v.processDelete()
	} else {
		log.Error().Str("transaction", v.VSTP.Schedule.TransactionType).Msg("Unhandled VSTP transaction")
	}
}

func (v *VSTPMessage) processCreate() {
	now := time.Now()
	var updateOperations []mongo.WriteModel

	for _, scheduleSegment := range v.VSTP.Schedule.ScheduleSegment {
		if !cif.IsValidPassengerJourney(scheduleSegment.TrainCategory, scheduleSegment.ATOC) {
			continue
		}

		journeyID := fmt.Sprintf("gb-rail-vstp-%s:%s:%s:%s", v.VSTP.Schedule.TrainUID, v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, v.VSTP.Schedule.STP)

		// Convert it to a CIF Definition Set
		startDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.StartDate)
		if err != nil {
			log.Error().Err(err).Msg("invalid vstp start date")
			continue
		}
		endDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.EndDate)
		if err != nil {
			log.Error().Err(err).Msg("invalid vstp end date")
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
					ScheduledDepartureTime: location.ScheduledDepartureTime,
					PublicDepartureTime:    location.PublicDepartureTime,
					Platform:               location.Platform,
					Activity:               location.Activity,
				}
			} else if index == len(scheduleSegment.ScheduleLocations)-1 {
				trainDefinitionSet.TerminatingLocation = cif.TerminatingLocation{
					Location:             location.Location.Tiploc.ID,
					ScheduledArrivalTime: location.ScheduledArrivalTime,
					PublicArrivalTime:    location.PublicArrivalTime,
					Platform:             location.Platform,
					Path:                 location.Path,
					Activity:             location.Activity,
				}
			} else {
				trainDefinitionSet.IntermediateLocations = append(trainDefinitionSet.IntermediateLocations, &cif.IntermediateLocation{
					Location:               location.Location.Tiploc.ID,
					ScheduledArrivalTime:   location.ScheduledArrivalTime,
					ScheduledDepartureTime: location.ScheduledDepartureTime,
					PublicArrivalTime:      location.PublicArrivalTime,
					PublicDepartureTime:    location.PublicDepartureTime,
					Platform:               location.Platform,
					Activity:               location.Activity,
				})
			}
		}

		// Convert this to ctdf
		cifDoc := cif.CommonInterfaceFormat{}
		journey := cifDoc.CreateJourneyFromTraindef(journeyID, trainDefinitionSet)
		journey.DataSource = &ctdf.DataSource{
			OriginalFormat: "JSON-CIF",
			Provider:       "Network Rail UK",
			DatasetID:      "gb-networkrail-vstp",
			Timestamp:      fmt.Sprintf("%d", now.Unix()),
		}
		journey.Expiry = endDate.Add(48 * time.Hour)

		cacheBustJourney(journey)

		// Insert into DB
		bsonRep, _ := bson.Marshal(bson.M{"$set": journey})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": journeyID})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		updateOperations = append(updateOperations, updateModel)

		log.Info().Str("journeyid", journeyID).Msg("Created new VSTP journey")

		// Create information alert about it being a short notice journey
		railutils.CreateServiceAlert(ctdf.ServiceAlert{
			PrimaryIdentifier:    fmt.Sprintf("gb-networkrail-vstpcreate-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
			CreationDateTime:     now,
			ModificationDateTime: now,

			DataSource: &ctdf.DataSource{
				OriginalFormat: "JSON-CIF",
				Provider:       "Network Rail UK",
				DatasetID:      "gb-networkrail-vstp",
				Timestamp:      fmt.Sprintf("%d", now.Unix()),
			},

			AlertType: ctdf.ServiceAlertTypeInformation,

			Text: "This timetable entry was added at short notice and is not part of a regularly scheduled service",

			MatchedIdentifiers: []string{journeyID},

			ValidFrom:  startDate,
			ValidUntil: endDate.Add(24 * time.Hour),
		})
	}

	if len(updateOperations) > 0 {
		// TODO we also need to clear any stop journey caches

		journeysCollection := database.GetCollection("journeys")
		_, err := journeysCollection.BulkWrite(context.Background(), updateOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
		}
	}
}

func (v *VSTPMessage) processDelete() {
	now := time.Now()

	var journey *ctdf.Journey

	journeysCollection := database.GetCollection("journeys")
	cursor := journeysCollection.FindOne(context.Background(), bson.M{
		"datasource.datasetid":      bson.M{"$in": bson.A{"gb-nationalrail-timetable", "gb-networkrail-vstp"}},
		"otheridentifiers.TrainUID": v.VSTP.Schedule.TrainUID,
	})

	err := cursor.Decode(&journey)

	if err == nil && journey != nil {
		// TODO also make it actually disappear/be cancelled via realtime_journey

		startDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.StartDate)
		if err != nil {
			log.Error().Err(err).Msg("invalid vstp start date")
			return
		}
		endDate, err := time.Parse("2006-01-02", v.VSTP.Schedule.EndDate)
		if err != nil {
			log.Error().Err(err).Msg("invalid vstp end date")
			return
		}

		var matchedIdentifiers []string

		// For each day between the start & end date that day of week is part of day runs
		// Add that day instance of to the list of matching identifiers
		for d := startDate; d.Before(endDate) || d.Equal(endDate); d = d.AddDate(0, 0, 1) {
			if string(v.VSTP.Schedule.DayRuns[convertWeekday(d)]) == "1" {
				ident := fmt.Sprintf("DAYINSTANCEOF:%s:%s", d.Format("2006-01-02"), journey.PrimaryIdentifier)

				matchedIdentifiers = append(matchedIdentifiers, ident)
			}
		}

		railutils.CreateServiceAlert(ctdf.ServiceAlert{
			PrimaryIdentifier:    fmt.Sprintf("gb-networkrail-vstpdelete-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
			CreationDateTime:     now,
			ModificationDateTime: now,

			DataSource: &ctdf.DataSource{
				OriginalFormat: "JSON-CIF",
				Provider:       "Network Rail UK",
				DatasetID:      "gb-networkrail-vstp",
				Timestamp:      fmt.Sprintf("%d", now.Unix()),
			},

			AlertType: ctdf.ServiceAlertTypeJourneyCancelled,

			Text: "Timetable entry was deleted by rail operator",

			MatchedIdentifiers: matchedIdentifiers,

			ValidFrom:  startDate,
			ValidUntil: endDate.Add(24 * time.Hour),
		})

		log.Info().
			Str("trainuid", v.VSTP.Schedule.TrainUID).
			Str("journey", journey.PrimaryIdentifier).
			Str("start", v.VSTP.Schedule.StartDate).
			Str("end", v.VSTP.Schedule.EndDate).
			Str("days", v.VSTP.Schedule.DayRuns).
			Msg("Creating service alert for VSTP delete")
	} else {
		log.Error().Str("trainuid", v.VSTP.Schedule.TrainUID).Msg("Failed to find journey for VSTP Delete")
	}
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
