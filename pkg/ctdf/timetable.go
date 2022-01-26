package ctdf

import (
	"time"
)

type TimetableRecord struct {
	Journey            *Journey `groups:"basic"`
	DestinationDisplay string   `groups:"basic"`

	Time time.Time `groups:"basic"`
}

func GenerateTimetableFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time) []*TimetableRecord {
	timetable := []*TimetableRecord{}

	for _, journey := range journeys {
		var stopDeperatureTime time.Time
		var destinationDisplay string

		for _, path := range journey.Path {
			if path.OriginStopRef == stopRef {
				refTime := path.OriginDepartureTime
				stopDeperatureTime = path.OriginDepartureTime
				stopDeperatureTime = time.Date(
					dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), refTime.Location(),
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

			timetable = append(timetable, &TimetableRecord{
				Journey:            journey,
				Time:               stopDeperatureTime,
				DestinationDisplay: destinationDisplay,
			})
		}
	}

	return timetable
}
