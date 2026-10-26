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
	// Performance Review [Set L] [PR112] [P1] (realtime): ActualTimestamp is ignored and receipt
	// time becomes both observed arrival/departure time and freshness. Catch-up
	// therefore makes old movements appear current and can regress next-stop state.
	// Parse source event time, retain ingest time separately, and deduplicate/order
	// per run and call while honoring CorrectionInd. Use planned-call identity to
	// distinguish repeat visits before dropping apparently stale/replayed updates.
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
	// Performance Review [Set J] [PR113] [P2] (realtime): After resolving Redis state, each movement
	// with a scheduled-journey reference reads the full MongoDB Journey, although the
	// stored runtime path already has stop references. Use that compact path where
	// sufficient, or cache immutable static paths/occurrence indices by timetable version.
	// Refresh on VSTP changes and fetch only missing fields; measure static reads per
	// accepted movement. Synthetic journeys already skip this static read.
	realtimestore.LoadStaticJourney(context.Background(), realtimeJourney)

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
		matchedJourneyStop := false

		for pathIndex, path := range realtimeJourney.Journey.Path {
			if path.OriginStopRef == locationStop.PrimaryIdentifier || util.ContainsString(locationStop.OtherIdentifiers, path.OriginStopRef) {
				matchedJourneyStop = true
				journeyStop := realtimeJourney.RealtimeStop(path.OriginStopRef, pathIndex)

				if journeyStop != nil && !journeyStop.DepartureTime.IsZero() && journeyStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
					continue
				}
				realtimeJourney.DepartedStopRef = path.OriginStopRef
				realtimeJourney.NextStopRef = path.DestinationStopRef
				realtimeJourney.NextStopIndex = pathIndex + 1
				realtimeJourney.DepartedStop = path.OriginStop
				realtimeJourney.NextStop = path.DestinationStop

				if journeyStop == nil {
					journeyStop = &ctdf.RealtimeJourneyStops{StopRef: path.OriginStopRef, JourneyStopIndex: pathIndex}
				}
				journeyStop.DepartureTime = now
				journeyStop.TimeType = ctdf.RealtimeJourneyStopTimeHistorical
				realtimeJourney.SetRealtimeStop(journeyStop)

				break
			}
		}

		description := fmt.Sprintf("Passed %s", locationStop.PrimaryName)
		if matchedJourneyStop {
			description = fmt.Sprintf("Departed %s", locationStop.PrimaryName)
		}
		realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, description)
	} else if m.EventType == "ARRIVAL" {
		matchedJourneyStop := false
		for pathIndex, path := range realtimeJourney.Journey.Path {
			journeyStopIndex := pathIndex + 1
			if path.DestinationStopRef != locationStop.PrimaryIdentifier && !util.ContainsString(locationStop.OtherIdentifiers, path.DestinationStopRef) {
				continue
			}
			matchedJourneyStop = true
			journeyStop := realtimeJourney.RealtimeStop(path.DestinationStopRef, journeyStopIndex)
			if journeyStop != nil && !journeyStop.ArrivalTime.IsZero() && journeyStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
				continue
			}
			if journeyStop == nil {
				journeyStop = &ctdf.RealtimeJourneyStops{StopRef: path.DestinationStopRef, JourneyStopIndex: journeyStopIndex}
			}
			journeyStop.ArrivalTime = now

			realtimeJourney.SetRealtimeStop(journeyStop)
			break
		}

		description := fmt.Sprintf("Passing %s", locationStop.PrimaryName)
		if matchedJourneyStop {
			description = fmt.Sprintf("Arrived at %s", locationStop.PrimaryName)
		}
		realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, description)
		if locationStop.Location != nil {
			realtimeJourney.VehicleLocation = *locationStop.Location
		}

		// If we've arrived at the end, then it's not actively tracked anymore
		if locationStop.PrimaryIdentifier == realtimeJourney.Journey.Path[len(realtimeJourney.Journey.Path)-1].DestinationStopRef {
			realtimeJourney.ActivelyTracked = false
			realtimeJourney.TimeoutDurationMinutes = 15
		}
	}

	// Create update
	// Performance Review [Set L] [PR114] [P1] (realtime): This replaces the complete shared run
	// snapshot after a separate read. Concurrent Darwin status/schedule writes can
	// be erased by this older NROD copy, including platform/cancellation changes.
	// Define field ownership and use atomic patches or compare-and-swap with merge
	// retries across processors/processes; a local worker mutex is insufficient.
	// Return write errors and derive events from the successfully committed state.
	realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)

	log.Debug().
		Str("trainid", m.TrainID).
		Str("eventtype", m.EventType).
		Str("stanox", m.LocationStanox).
		Str("stop", locationStop.PrimaryIdentifier).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train movement")
}
