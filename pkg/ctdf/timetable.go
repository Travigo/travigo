package ctdf

import (
	"time"
)

type TimetableRecord struct {
	Journey            *Journey `groups:"basic"`
	DestinationDisplay string   `groups:"basic"`

	Time time.Time `groups:"basic"`
}

func GenerateTimetableFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time, realtimeTimeframe string) []*TimetableRecord {
	timetable := []*TimetableRecord{}

	for _, journey := range journeys {
		var stopDeperatureTime time.Time
		var destinationDisplay string

		journey.GetRealtimeJourney()

		for _, path := range journey.Path {
			if path.OriginStopRef == stopRef {
				refTime := path.OriginDepartureTime

				// Use the realtime estimated stop time based if realtime is available
				if journey.RealtimeJourney != nil && journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
					refTime = journey.RealtimeJourney.Stops[path.OriginStopRef].ArrivalTime // TODO: this should be departure time
				}

				stopDeperatureTime = time.Date(
					dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
				)

				destinationDisplay = path.DestinationDisplay
				break
			}
		}

		if stopDeperatureTime.Before(dateTime) {
			continue
		}

		availability := journey.Availability

		if availability.MatchDate(dateTime) {
			journey.GetReferences()
			journey.GetRealtimeJourney()

			timetable = append(timetable, &TimetableRecord{
				Journey:            journey,
				Time:               stopDeperatureTime,
				DestinationDisplay: destinationDisplay,
			})
		}
	}

	return timetable
}
