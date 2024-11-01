package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
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

			StartDate string `json:"schedule_start_date"`
			EndDate   string `json:"schedule_end_date"`
			DayRuns   string `json:"schedule_days_runs"`
		} `json:"schedule"`
	} `json:"VSTPCIFMsgV1"`
}

func (v *VSTPMessage) Process(stompClient *StompClient) {
	now := time.Now()

	if v.VSTP.Schedule.TransactionType == "Create" {
		log.Error().Msg("Unhandled create")
	} else if v.VSTP.Schedule.TransactionType == "Delete" {
		var journey *ctdf.Journey

		journeysCollection := database.GetCollection("journeys")
		cursor := journeysCollection.FindOne(context.Background(), bson.M{
			"datasource.datasetid":      "gb-nationalrail-timetable",
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
				if string(v.VSTP.Schedule.DayRuns[d.Weekday()-1]) == "1" {
					ident := fmt.Sprintf("DAYINSTANCEOF:%s:%s", d.Format("2006-01-02"), journey.PrimaryIdentifier)

					matchedIdentifiers = append(matchedIdentifiers, ident)
				}
			}

			railutils.CreateServiceAlert(ctdf.ServiceAlert{
				PrimaryIdentifier:    fmt.Sprintf("gb-networkrail-vstpdelete-%s:%s:%s", v.VSTP.Schedule.StartDate, v.VSTP.Schedule.EndDate, journey.PrimaryIdentifier),
				CreationDateTime:     now,
				ModificationDateTime: now,

				DataSource: &ctdf.DataSource{
					DatasetID: "gb-networkrail-vstp",
				},

				AlertType: ctdf.ServiceAlertTypeJourneyPartiallyCancelled,

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
	} else {
		log.Error().Str("transaction", v.VSTP.Schedule.TransactionType).Msg("Unhandled VSTP transaction")
	}
}

type ScheduleSegment struct {
	ScheduleLocations []ScheduleLocation `json:"schedule_location"`

	SignallingID     string `json:"signalling_id"`
	TrainServiceCode string `json:"CIF_train_service_code"`
	TrainCategory    string `json:"CIF_train_category"`
	Speed            string `json:"CIF_speed"`
	PowerType        string `json:"CIF_power_type"`
	CourseIndicator  string `json:"CIF_course_indicator"`
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
