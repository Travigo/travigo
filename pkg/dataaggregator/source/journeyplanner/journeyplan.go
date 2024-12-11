package journeyplanner

import (
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"go.mongodb.org/mongo-driver/bson"
)

func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	// THIS IS A BASIC NO CHANGE PLANNER

	// Do a departure board query
	var departureBoard []*ctdf.DepartureBoard

	departureBoard, err := dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          q.OriginStop,
		Count:         q.Count,
		StartDateTime: q.StartDateTime,
		Filter:        &bson.M{"path.destinationstopref": q.DestinationStop.PrimaryIdentifier},
	})

	if err != nil {
		return nil, err
	}

	// Sort departures by DepartureBoard time
	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})

	// Once sorted cut off any records higher than our max count
	if len(departureBoard) > q.Count {
		departureBoard = departureBoard[:q.Count]
	}

	// Turn the departure board into a journey plan
	journeyPlanResults := &ctdf.JourneyPlanResults{
		JourneyPlans:    []ctdf.JourneyPlan{},
		OriginStop:      *q.OriginStop,
		DestinationStop: *q.DestinationStop,
	}

	for _, departure := range departureBoard {
		startTime := departure.Time
		var arrivalTime time.Time

		for _, item := range departure.Journey.Path {
			if item.DestinationStopRef == q.DestinationStop.PrimaryIdentifier {
				refTime := item.DestinationArrivalTime
				dateTime := q.StartDateTime
				arrivalTime = time.Date(
					dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
				)

				if arrivalTime.Before(startTime) {
					arrivalTime = arrivalTime.Add(24 * time.Hour)
				}
				break
			}
		}

		journeyPlan := ctdf.JourneyPlan{
			RouteItems: []ctdf.JourneyPlanRouteItem{
				{
					Journey:            *departure.Journey,
					JourneyType:        *departure.Type,
					OriginStopRef:      q.OriginStop.PrimaryIdentifier,
					DestinationStopRef: q.DestinationStop.PrimaryIdentifier,
					StartTime:          startTime,
					ArrivalTime:        arrivalTime,
				},
			},
			StartTime:   startTime,
			ArrivalTime: arrivalTime,
			Duration:    arrivalTime.Sub(startTime),
		}

		journeyPlanResults.JourneyPlans = append(journeyPlanResults.JourneyPlans, journeyPlan)
	}

	return journeyPlanResults, nil
}
