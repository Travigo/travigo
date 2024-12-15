package journeyplanner

import (
	"sort"
	"time"

	"github.com/kr/pretty"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"go.mongodb.org/mongo-driver/bson"

	"golang.org/x/exp/slices"
)

func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	// THIS IS A BASIC NO CHANGE PLANNER

	// Do a departure board query
	var departureBoard []*ctdf.DepartureBoard

	departureBoard, err := dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          q.OriginStop,
		Count:         q.Count * 10,
		StartDateTime: q.StartDateTime,
		Filter:        &bson.M{"path.destinationstopref": bson.M{"$in": append(q.DestinationStop.OtherIdentifiers, q.DestinationStop.PrimaryIdentifier)}},
	})

	if err != nil {
		return nil, err
	}

	// Sort departures by DepartureBoard time
	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})

	// Turn the departure board into a journey plan
	journeyPlanResults := &ctdf.JourneyPlanResults{
		JourneyPlans:    []ctdf.JourneyPlan{},
		OriginStop:      *q.OriginStop,
		DestinationStop: *q.DestinationStop,
	}

	currentFound := 0

	for _, departure := range departureBoard {
		if currentFound >= q.Count {
			break
		}

		startTime := departure.Time
		var arrivalTime time.Time

		seenOrigin := false

		for _, item := range departure.Journey.Path {
			pretty.Println(item.OriginStopRef, q.OriginStop.PrimaryIdentifier, q.OriginStop.OtherIdentifiers)
			if item.OriginStopRef == q.OriginStop.PrimaryIdentifier || slices.Contains[[]string](q.OriginStop.OtherIdentifiers, item.OriginStopRef) {
				pretty.Println("cum")
				seenOrigin = true
			}

			if item.DestinationStopRef == q.DestinationStop.PrimaryIdentifier || slices.Contains[[]string](q.DestinationStop.OtherIdentifiers, item.DestinationStopRef) {
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

		// If we've not seen origin by the time we've seen destination then this journey is running in the wrong direction
		if !seenOrigin {
			continue
		}

		journeyPlan := ctdf.JourneyPlan{
			RouteItems: []ctdf.JourneyPlanRouteItem{
				{
					Journey:            *departure.Journey,
					JourneyType:        departure.Type,
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
		currentFound += 1
	}

	return journeyPlanResults, nil
}
