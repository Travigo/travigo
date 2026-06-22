package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/util"
)

type TrustMovement struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`

	TimestampGBTT          string `json:"gbtt_timestamp"`
	PlannedTimestamp       string `json:"planned_timestamp"`
	ActualTimestamp        string `json:"actual_timestamp"`
	OriginalLocationStanox string `json:"original_loc_stanox"`
	OriginalLOCTimestamp   string `json:"original_loc_timestamp"`
	TimetableVariation     string `json:"timetable_variation"`
	CurrentTrainID         string `json:"current_train_id"`
	DelayMonitoringPoint   string `json:"delay_monitoring_point"`
	NextReportRunTime      string `json:"next_report_run_time"`
	ReportingStanox        string `json:"reporting_stanox"`
	CorrectionInd          string `json:"correction_ind"`
	EventSource            string `json:"event_source"`
	TrainFileAddress       string `json:"train_file_address"`
	Platform               string `json:"platform"`
	DivisionCode           string `json:"division_code"`
	TrainTerminated        string `json:"train_terminated"`
	Offroute               string `json:"offroute_ind"`
	VariationStatus        string `json:"variation_status"`
	TrainServiceCode       string `json:"train_service_code"`
	LocationStanox         string `json:"loc_stanox"`
	AutoExpected           string `json:"auto_expected"`
	Direction              string `json:"direction_ind"`
	Route                  string `json:"route"`
	PlannedEventType       string `json:"planned_event_type"`
	NextReportStanox       string `json:"next_report_stanox"`
	Line                   string `json:"line_ind"`
}

func (m *TrustMovement) Process(stompClient *StompClient) {
	now := time.Now()

	realtimeJourney, err := realtimestore.FindByMapping(context.Background(), "TrainID", m.TrainID)
	if err != nil {
		log.Error().Err(err).Str("trainid", m.TrainID).Str("toc", m.OperatorID).Msg("Error finding Realtime Journey for train movement")
		return
	}
	if realtimeJourney == nil {
		log.Debug().Str("trainid", m.TrainID).Str("toc", m.OperatorID).Msg("Could not find Realtime Journey for train movement")
		return
	}

	if realtimeJourney.Journey == nil {
		log.Error().Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).Msg("Somehow realtime journey had no journey attached?")
		return
	}

	realtimeJourney.ModificationDateTime = now
	realtimeJourney.ActivelyTracked = m.TrainTerminated != "true"

	locationStop := stompClient.StopCache.Get(fmt.Sprintf("gb-stanox-%s", m.LocationStanox))
	if locationStop == nil {
		log.Debug().Str("stanox", m.LocationStanox).Msg("Cannot find stop for movement")
		return
	}

	if m.EventType == "DEPARTURE" {
		for _, path := range realtimeJourney.Journey.Path {
			if path.OriginStopRef == locationStop.PrimaryIdentifier || util.ContainsString(locationStop.OtherIdentifiers, path.OriginStopRef) {
				realtimeJourney.DepartedStopRef = path.OriginStopRef
				realtimeJourney.NextStopRef = path.DestinationStopRef
				realtimeJourney.DepartedStop = path.OriginStop
				realtimeJourney.NextStop = path.DestinationStop

				realtimeJourney.Stops[locationStop.PrimaryIdentifier].StopRef = locationStop.PrimaryIdentifier
				realtimeJourney.Stops[locationStop.PrimaryIdentifier].DepartureTime = now
				realtimeJourney.Stops[locationStop.PrimaryIdentifier].TimeType = ctdf.RealtimeJourneyStopTimeHistorical

				break
			}
		}

		realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, fmt.Sprintf("Departed %s", locationStop.PrimaryName))
	} else if m.EventType == "ARRIVAL" {
		realtimeJourney.Stops[locationStop.PrimaryIdentifier].StopRef = locationStop.PrimaryIdentifier
		realtimeJourney.Stops[locationStop.PrimaryIdentifier].ArrivalTime = now

		realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, fmt.Sprintf("Arrived at %s", locationStop.PrimaryName))

		// If we've arrived at the end, then it's not actively tracked anymore
		if locationStop.PrimaryIdentifier == realtimeJourney.Journey.Path[len(realtimeJourney.Journey.Path)-1].DestinationStopRef {
			realtimeJourney.ActivelyTracked = false
			realtimeJourney.TimeoutDurationMinutes = 15
		}
	}

	// Create update
	realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)

	log.Debug().
		Str("trainid", m.TrainID).
		Str("eventtype", m.EventType).
		Str("stanox", m.LocationStanox).
		Str("stop", locationStop.PrimaryIdentifier).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train movement")
}
