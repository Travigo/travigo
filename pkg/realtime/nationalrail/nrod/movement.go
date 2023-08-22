package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
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

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection.FindOne(context.Background(), bson.M{"otheridentifiers.TrainID": m.TrainID}).Decode(&realtimeJourney)
	if realtimeJourney == nil {
		log.Debug().Str("trainid", m.TrainID).Str("toc", m.OperatorID).Msg("Could not find Realtime Journey for train movement")
		return
	}

	updateMap := bson.M{
		"modificationdatetime": now,
		"activelytracked":      m.TrainTerminated != "true",
	}

	locationStop := stompClient.StopCache.Get("STANOX", m.LocationStanox)
	if locationStop == nil {
		return
	}

	if m.EventType == "DEPARTURE" {
		for _, path := range realtimeJourney.Journey.Path {
			if path.OriginStopRef == locationStop.PrimaryIdentifier {
				realtimeJourney.DepartedStopRef = path.OriginStopRef
				realtimeJourney.NextStopRef = path.DestinationStopRef
				realtimeJourney.DepartedStop = path.OriginStop
				realtimeJourney.NextStop = path.DestinationStop

				updateMap[fmt.Sprintf("stops.%s.stopref", locationStop.PrimaryIdentifier)] = locationStop.PrimaryIdentifier
				updateMap[fmt.Sprintf("stops.%s.departuretime", locationStop.PrimaryIdentifier)] = now
				updateMap[fmt.Sprintf("stops.%s.timetype", locationStop.PrimaryIdentifier)] = ctdf.RealtimeJourneyStopTimeHistorical

				break
			}
		}
	} else if m.EventType == "ARRIVAL" {
		updateMap[fmt.Sprintf("stops.%s.stopref", locationStop.PrimaryIdentifier)] = locationStop.PrimaryIdentifier
		updateMap[fmt.Sprintf("stops.%s.arrivaltime", locationStop.PrimaryIdentifier)] = now
	}

	updateMap["departedstopref"] = realtimeJourney.DepartedStopRef
	updateMap["nextstopref"] = realtimeJourney.NextStopRef
	updateMap["departedstop"] = realtimeJourney.DepartedStop
	updateMap["nextstop"] = realtimeJourney.NextStop

	// Create update
	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(bson.M{"primaryidentifier": realtimeJourney.PrimaryIdentifier})
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	stompClient.Queue.Add(updateModel)
	// pretty.Println(updateMap)

	log.Debug().
		Str("trainid", m.TrainID).
		Str("eventtype", m.EventType).
		Str("stanox", m.LocationStanox).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train movement")
}
