package nrod

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/railtd"
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
	observedAt, observed := railEventTime(m.ActualTimestamp, now)
	stopTime := now
	if observed {
		stopTime = observedAt
	}

	realtimeJourney, err := realtimestore.FindByMapping(context.Background(), "TrainID", m.TrainID)
	if err != nil {
		log.Error().Err(err).Str("trainid", m.TrainID).Str("toc", m.OperatorID).Msg("Error finding Realtime Journey for train movement")
		return
	}
	if realtimeJourney == nil || realtimeJourney.OtherIdentifiers["TrainID"] != m.TrainID {
		log.Debug().Str("trainid", m.TrainID).Str("toc", m.OperatorID).Msg("Could not find Realtime Journey for train movement")
		return
	}
	if realtimeJourney.Journey == nil {
		log.Error().Str("realtimejourneyid", realtimeJourney.PrimaryIdentifier).Msg("Somehow realtime journey had no journey attached?")
		return
	}
	// The stored path includes Darwin amendments and uses the same occurrence
	// indexes as the realtime stop records. Only hydrate older runs without a path.
	if len(realtimeJourney.Journey.Calls) == 0 {
		realtimestore.LoadStaticJourney(context.Background(), realtimeJourney)
	}

	realtimeJourney.ModificationDateTime = now
	realtimeJourney.ActivelyTracked = m.TrainTerminated != "true"

	locationStop := stompClient.StopCache.Get(fmt.Sprintf("gb-stanox-%s", m.LocationStanox))
	if locationStop == nil {
		if observed {
			m.recordObservation(stompClient, realtimeJourney, observedAt, nil, -1, nil)
		}
		log.Debug().Str("stanox", m.LocationStanox).Msg("Cannot find stop for movement")
		return
	}

	legIndex := -1
	matchedJourneyStop := false
	if m.EventType == "DEPARTURE" {

		pathIndex := -1
		for origin, destination := range realtimeJourney.Journey.LegCalls() {
			pathIndex++
			if origin.StopRef == locationStop.PrimaryIdentifier || util.ContainsString(locationStop.OtherIdentifiers, origin.StopRef) {
				matchedJourneyStop = true
				journeyStop := realtimeJourney.RealtimeStop(origin.StopRef, pathIndex)

				if journeyStop != nil && !journeyStop.DepartureTime.IsZero() && journeyStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
					continue
				}
				legIndex = pathIndex
				realtimeJourney.DepartedStopRef = origin.StopRef
				realtimeJourney.NextStopRef = destination.StopRef
				realtimeJourney.NextStopIndex = pathIndex + 1
				if origin.Stop == nil {
					origin.Stop = locationStop
				}
				if destination.Stop == nil {
					destination.GetStop()
				}
				realtimeJourney.DepartedStop = origin.Stop
				realtimeJourney.NextStop = destination.Stop

				if journeyStop == nil {
					journeyStop = &ctdf.RealtimeJourneyStops{StopRef: origin.StopRef, JourneyStopIndex: pathIndex}
				}
				journeyStop.DepartureTime = stopTime
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
		pathIndex := -1
		for _, destination := range realtimeJourney.Journey.LegCalls() {
			pathIndex++
			journeyStopIndex := pathIndex + 1
			if destination.StopRef != locationStop.PrimaryIdentifier && !util.ContainsString(locationStop.OtherIdentifiers, destination.StopRef) {
				continue
			}
			matchedJourneyStop = true
			journeyStop := realtimeJourney.RealtimeStop(destination.StopRef, journeyStopIndex)
			if journeyStop != nil && !journeyStop.ArrivalTime.IsZero() && journeyStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
				continue
			}
			if journeyStop == nil {
				journeyStop = &ctdf.RealtimeJourneyStops{StopRef: destination.StopRef, JourneyStopIndex: journeyStopIndex}
			}
			journeyStop.ArrivalTime = stopTime

			realtimeJourney.SetRealtimeStop(journeyStop)
			break
		}

		description := fmt.Sprintf("Passing %s", locationStop.PrimaryName)
		if matchedJourneyStop {
			description = fmt.Sprintf("Arrived at %s", locationStop.PrimaryName)
		}
		realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, description)

		// If we've arrived at the end, then it's not actively tracked anymore
		if len(realtimeJourney.Journey.Calls) > 0 && locationStop.PrimaryIdentifier == realtimeJourney.Journey.Calls[len(realtimeJourney.Journey.Calls)-1].StopRef {
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
	if err := realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney); err != nil {
		log.Error().Err(err).Msg("Failed to save TRUST movement")
		return
	}
	if observed {
		var point *ctdf.Location
		var operationalStop *ctdf.Stop
		isPositionReport := m.EventType == "ARRIVAL" || m.EventType == "DEPARTURE" || m.EventType == "PASS"
		if !matchedJourneyStop && m.Offroute != "true" && isPositionReport {
			for origin := range realtimeJourney.Journey.LegCalls() {
				if matchesPassingStop(origin.Outgoing, locationStop) {
					operationalStop = locationStop
					break
				}
			}
		}
		// TRUST identifies the run and reports the actual location. An express
		// train can report at a timing point absent from its published path.
		if isPositionReport && m.Offroute != "true" && railtd.ValidPoint(locationStop.Location) {
			point = locationStop.Location
		}
		m.recordObservation(stompClient, realtimeJourney, observedAt, point, legIndex, operationalStop)
	}

	log.Debug().
		Str("trainid", m.TrainID).
		Str("eventtype", m.EventType).
		Str("stanox", m.LocationStanox).
		Str("stop", locationStop.PrimaryIdentifier).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train movement")
}

func railEventTime(value string, now time.Time) (time.Time, bool) {
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	at := time.UnixMilli(ms).UTC()
	return at, !at.After(now.Add(time.Minute)) && !at.Before(now.Add(-24*time.Hour))
}

func matchesPassingStop(leg *ctdf.JourneyLeg, stop *ctdf.Stop) bool {
	if leg == nil || stop == nil {
		return false
	}
	for _, ref := range leg.PassingPath {
		if ref == stop.PrimaryIdentifier || util.ContainsString(stop.OtherIdentifiers, ref) {
			return true
		}
	}
	return false
}

func (m *TrustMovement) recordObservation(client *StompClient, journey *ctdf.RealtimeJourney, at time.Time, point *ctdf.Location, leg int, operationalStop *ctdf.Stop) {
	if client.td != nil {
		if origin, _ := journey.Journey.LegEndpoints(leg); origin != nil {
			index := 0
			for candidate := range journey.Journey.LegCalls() {
				if index != leg && candidate.StopRef == origin.StopRef {
					leg = -1
					break
				}
				index++
			}
		}
		if point == nil || m.CorrectionInd == "true" {
			leg = -1
		}
		client.td.trust(journey, m, at, leg, operationalStop)
	}
	if point == nil {
		return
	}
	observation := realtimestore.RailObservation{ObservedAt: at, Source: "TRUST", Confidence: "mapped"}
	if _, err := realtimestore.RecordRailObservation(context.Background(), journey, observation, point); err != nil {
		log.Warn().Err(err).Msg("Failed to record TRUST observation")
	}
}
