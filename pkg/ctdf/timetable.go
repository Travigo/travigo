package ctdf

import (
	"context"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type TimetableRecord struct {
	Journey            *Journey `groups:"basic"`
	DestinationDisplay string   `groups:"basic"`

	Time time.Time `groups:"basic"`

	RealtimeJourney *RealtimeJourney `groups:"basic"`
}

func GenerateTimetableFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time, realtimeTimeframe string) []*TimetableRecord {
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

			// Get the related RealtimeJourney
			realtimeJourneyIdentifier := fmt.Sprintf(RealtimeJourneyIDFormat, realtimeTimeframe, journey.PrimaryIdentifier)
			realtimeJourneysCollection := database.GetCollection("realtime_journeys")

			var realtimeJourney *RealtimeJourney
			realtimeJourneysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": realtimeJourneyIdentifier}).Decode(&realtimeJourney)

			timetable = append(timetable, &TimetableRecord{
				Journey:            journey,
				Time:               stopDeperatureTime,
				DestinationDisplay: destinationDisplay,
				RealtimeJourney:    realtimeJourney,
			})
		}
	}

	return timetable
}
