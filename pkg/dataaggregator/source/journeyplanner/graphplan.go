package journeyplanner

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/departuregraph"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

func coordinateOriginStop(location *ctdf.Location) *ctdf.Stop {
	stop := &ctdf.Stop{PrimaryIdentifier: "coordinate-origin", PrimaryName: "Selected location", Active: true}
	if location == nil || len(location.Coordinates) != 2 {
		return stop
	}
	stop.PrimaryIdentifier = fmt.Sprintf("coordinate-origin:%.6f,%.6f", location.Coordinates[0], location.Coordinates[1])
	stop.Location = &ctdf.Location{Type: "Point", Coordinates: append([]float64(nil), location.Coordinates...)}
	return stop
}

// JourneyPlanQuery is intentionally a thin service boundary. The journey graph
// owns topology and route calculation; web-api only hydrates the compact
// journey references required by the public CTDF response.
func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	if s.JourneyGraph == nil {
		return nil, fmt.Errorf("journey graph service is not configured")
	}
	if q.DestinationStop == nil {
		return nil, fmt.Errorf("journey plan requires a destination stop")
	}
	if q.OriginStop == nil && q.OriginLocation == nil {
		return nil, fmt.Errorf("journey plan requires an origin stop or location")
	}

	request := departuregraph.PlanRequest{
		DestinationRefs:           q.DestinationStop.GetAllStopIDs(),
		StartDateTime:             q.StartDateTime,
		Count:                     q.Count,
		MaxChanges:                q.MaxChanges,
		MaxJourneyDurationSeconds: int(q.MaxJourneyDuration / time.Second),
		MaxTransferDistanceMetres: q.MaxTransferDistanceMetres,
		OriginLocationStopCount:   q.OriginLocationStopCount,
		MaxExpandedLabels:         q.MaxExpandedLabels,
		MaxSearchDurationMillis:   int(q.MaxSearchDuration / time.Millisecond),
	}
	if q.OriginStop != nil {
		request.OriginRefs = q.OriginStop.GetAllStopIDs()
	} else if q.OriginLocation != nil && len(q.OriginLocation.Coordinates) == 2 {
		request.OriginLocation = &departuregraph.PlanLocation{
			Longitude: q.OriginLocation.Coordinates[0],
			Latitude:  q.OriginLocation.Coordinates[1],
		}
	}

	graphResult, err := s.JourneyGraph.Plan(context.Background(), request)
	if err != nil {
		return nil, err
	}
	journeys, err := hydrateGraphJourneys(graphResult)
	if err != nil {
		return nil, err
	}
	origin := q.OriginStop
	if origin == nil {
		origin = coordinateOriginStop(q.OriginLocation)
	}
	result := &ctdf.JourneyPlanResults{
		JourneyPlans:          make([]ctdf.JourneyPlan, 0, len(graphResult.Plans)),
		OriginStop:            *origin,
		DestinationStop:       *q.DestinationStop,
		SearchTruncated:       graphResult.SearchTruncated,
		SearchTruncatedReason: graphResult.SearchTruncatedReason,
	}
	for _, graphPlan := range graphResult.Plans {
		plan := ctdf.JourneyPlan{
			RouteItems:  make([]ctdf.JourneyPlanRouteItem, 0, len(graphPlan.Legs)),
			StartTime:   graphPlan.StartTime,
			ArrivalTime: graphPlan.ArrivalTime,
			Duration:    graphPlan.ArrivalTime.Sub(graphPlan.StartTime),
		}
		valid := true
		for _, leg := range graphPlan.Legs {
			item := ctdf.JourneyPlanRouteItem{
				Type:                     leg.Type,
				TransferType:             leg.TransferType,
				OriginStopRef:            leg.OriginStopRef,
				DestinationStopRef:       leg.DestinationStopRef,
				StartTime:                leg.StartTime,
				ArrivalTime:              leg.ArrivalTime,
				DistanceMetres:           leg.DistanceMetres,
				WalkDurationSeconds:      leg.WalkDurationSeconds,
				MinChangeDurationSeconds: leg.MinChangeDurationSeconds,
				TotalDurationSeconds:     leg.TotalDurationSeconds,
			}
			if leg.JourneyRef != "" {
				item.Journey = journeys[leg.JourneyRef]
				item.JourneyType = ctdf.DepartureBoardRecordTypeScheduled
				if item.Journey == nil {
					valid = false
					break
				}
			}
			plan.RouteItems = append(plan.RouteItems, item)
		}
		if valid {
			result.JourneyPlans = append(result.JourneyPlans, plan)
		}
	}
	return result, nil
}

func hydrateGraphJourneys(result departuregraph.PlanResponse) (map[string]*ctdf.Journey, error) {
	identifiers := make([]string, 0)
	seen := map[string]bool{}
	for _, plan := range result.Plans {
		for _, leg := range plan.Legs {
			if leg.JourneyRef != "" && !seen[leg.JourneyRef] {
				seen[leg.JourneyRef] = true
				identifiers = append(identifiers, leg.JourneyRef)
			}
		}
	}
	journeys := make(map[string]*ctdf.Journey, len(identifiers))
	if len(identifiers) == 0 {
		return journeys, nil
	}
	cursor, err := database.GetCollection(database.JourneysCollectionName).Find(context.Background(), bson.M{
		"primaryidentifier": bson.M{"$in": identifiers},
	})
	if err != nil {
		return nil, err
	}
	for cursor.Next(context.Background()) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return nil, err
		}
		journeys[journey.PrimaryIdentifier] = &journey
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close(context.Background())
		return nil, err
	}
	_ = cursor.Close(context.Background())
	realtime, err := realtimestore.FindCurrentForJourneyIDs(context.Background(), identifiers)
	if err == nil {
		for identifier, realtimeJourney := range realtime {
			if journeys[identifier] != nil {
				journeys[identifier].RealtimeJourney = realtimeJourney
			}
		}
	}
	return journeys, nil
}
