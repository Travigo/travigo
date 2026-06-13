package railutils

import (
	"context"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

func CacheRealtimeJourney(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney, updateMap bson.M) {
	if realtimeJourney == nil {
		return
	}

	ApplyRealtimeJourneyUpdate(realtimeJourney, updateMap)
	realtimestore.SetRealtimeJourney(ctx, realtimeJourney)
}

func ApplyRealtimeJourneyUpdate(realtimeJourney *ctdf.RealtimeJourney, updateMap bson.M) {
	if realtimeJourney == nil {
		return
	}

	for key, value := range updateMap {
		switch key {
		case "modificationdatetime":
			if modificationDateTime, ok := value.(time.Time); ok {
				realtimeJourney.ModificationDateTime = modificationDateTime
			}
		case "activelytracked":
			if activelyTracked, ok := value.(bool); ok {
				realtimeJourney.ActivelyTracked = activelyTracked
			}
		case "timeoutdurationminutes":
			if timeoutDurationMinutes, ok := value.(int); ok {
				realtimeJourney.TimeoutDurationMinutes = timeoutDurationMinutes
			}
		case "cancelled":
			if cancelled, ok := value.(bool); ok {
				realtimeJourney.Cancelled = cancelled
			}
		case "datasource":
			if datasource, ok := value.(*ctdf.DataSourceReference); ok {
				realtimeJourney.DataSource = datasource
			}
		case "datasource.timestamp":
			if timestamp, ok := value.(string); ok {
				if realtimeJourney.DataSource == nil {
					realtimeJourney.DataSource = &ctdf.DataSourceReference{}
				}
				realtimeJourney.DataSource.Timestamp = timestamp
			}
		case "vehiclelocationdescription":
			if description, ok := value.(string); ok {
				realtimeJourney.VehicleLocationDescription = description
			}
		case "departedstopref":
			if stopRef, ok := value.(string); ok {
				realtimeJourney.DepartedStopRef = stopRef
			}
		case "nextstopref":
			if stopRef, ok := value.(string); ok {
				realtimeJourney.NextStopRef = stopRef
			}
		case "occupancy":
			if occupancy, ok := value.(ctdf.RealtimeJourneyOccupancy); ok {
				realtimeJourney.Occupancy = occupancy
			}
		case "detailedrailinformation":
			if detailedRailInformation, ok := value.(ctdf.RealtimeJourneyDetailedRail); ok {
				realtimeJourney.DetailedRailInformation = detailedRailInformation
			}
		default:
			if strings.HasPrefix(key, "otheridentifiers.") {
				applyOtherIdentifierUpdate(realtimeJourney, strings.TrimPrefix(key, "otheridentifiers."), value)
				continue
			}

			if strings.HasPrefix(key, "stops.") {
				applyRealtimeStopUpdate(realtimeJourney, strings.TrimPrefix(key, "stops."), value)
			}
		}
	}
}

func applyOtherIdentifierUpdate(realtimeJourney *ctdf.RealtimeJourney, identifierName string, value interface{}) {
	if identifierName == "" {
		return
	}
	if realtimeJourney.OtherIdentifiers == nil {
		realtimeJourney.OtherIdentifiers = map[string]string{}
	}

	if identifierValue, ok := value.(string); ok {
		realtimeJourney.OtherIdentifiers[identifierName] = identifierValue
	}
}

func applyRealtimeStopUpdate(realtimeJourney *ctdf.RealtimeJourney, stopUpdatePath string, value interface{}) {
	if stopUpdatePath == "" {
		return
	}
	if realtimeJourney.Stops == nil {
		realtimeJourney.Stops = map[string]*ctdf.RealtimeJourneyStops{}
	}

	stopRef, stopField, hasField := strings.Cut(stopUpdatePath, ".")
	if stopRef == "" {
		return
	}

	if !hasField {
		switch stop := value.(type) {
		case *ctdf.RealtimeJourneyStops:
			realtimeJourney.Stops[stopRef] = stop
		case ctdf.RealtimeJourneyStops:
			stopCopy := stop
			realtimeJourney.Stops[stopRef] = &stopCopy
		}
		return
	}

	stop := realtimeJourney.Stops[stopRef]
	if stop == nil {
		stop = &ctdf.RealtimeJourneyStops{StopRef: stopRef}
		realtimeJourney.Stops[stopRef] = stop
	}

	switch stopField {
	case "stopref":
		if value, ok := value.(string); ok {
			stop.StopRef = value
		}
	case "platform":
		if value, ok := value.(string); ok {
			stop.Platform = value
		}
	case "arrivaltime":
		if value, ok := value.(time.Time); ok {
			stop.ArrivalTime = value
		}
	case "departuretime":
		if value, ok := value.(time.Time); ok {
			stop.DepartureTime = value
		}
	case "timetype":
		if value, ok := value.(ctdf.RealtimeJourneyStopTimeType); ok {
			stop.TimeType = value
		}
	case "cancelled":
		if value, ok := value.(bool); ok {
			stop.Cancelled = value
		}
	}
}
