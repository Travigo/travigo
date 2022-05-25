package ctdf

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type TimetableRecord struct {
	Journey            *Journey            `groups:"basic"`
	DestinationDisplay string              `groups:"basic"`
	Type               TimetableRecordType `groups:"basic"`

	Time time.Time `groups:"basic"`
}

type TimetableRecordType string

const (
	TimetableRecordTypeScheduled       TimetableRecordType = "Scheduled"
	TimetableRecordTypeRealtimeTracked                     = "RealtimeTracked"
	TimetableRecordTypeEstimated                           = "Estimated"
)

func GenerateTimetableFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time, realtimeTimeframe string, doEstimates bool) []*TimetableRecord {
	timetable := []*TimetableRecord{}
	journeysCollection := database.GetCollection("journeys")
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	journeys = FilterIdenticalJourneys(journeys)

	for _, journey := range journeys {
		var stopDeperatureTime time.Time
		var destinationDisplay string
		timetableRecordType := TimetableRecordTypeScheduled

		journey.GetRealtimeJourney(realtimeTimeframe)

		for _, path := range journey.Path {
			if path.OriginStopRef == stopRef {
				refTime := path.OriginDepartureTime

				// Use the realtime estimated stop time based if realtime is available
				if journey.RealtimeJourney != nil && journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
					refTime = journey.RealtimeJourney.Stops[path.OriginStopRef].DepartureTime
					timetableRecordType = TimetableRecordTypeRealtimeTracked
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

			// If the departure is within 45 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
			// We estimate the current vehicle realtime journey based on the Block Number
			stopDeperatureTimeFromNow := stopDeperatureTime.Sub(dateTime).Minutes()
			if doEstimates &&
				timetableRecordType == TimetableRecordTypeScheduled &&
				stopDeperatureTimeFromNow <= 45 && stopDeperatureTimeFromNow >= 0 &&
				journey.OtherIdentifiers["BlockNumber"] != "" {

				blockJourneys := []string{}
				cursor, _ := journeysCollection.Find(context.Background(), bson.M{"serviceref": journey.ServiceRef, "otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"]})

				for cursor.Next(context.TODO()) {
					var blockJourney Journey
					err := cursor.Decode(&blockJourney)
					if err != nil {
						log.Error().Err(err).Msg("Failed to decode Journey")
					}

					blockJourneys = append(blockJourneys, blockJourney.PrimaryIdentifier)
				}

				var blockRealtimeJourney *RealtimeJourney
				realtimeJourneysCollection.FindOne(context.Background(),
					bson.M{
						"journeyref": bson.M{
							"$in": blockJourneys,
						},
						"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
					}, &options.FindOneOptions{}).Decode(&blockRealtimeJourney)

				if blockRealtimeJourney != nil {
					// Ignore negative offsets as we assume bus will right itself when turning over
					if blockRealtimeJourney.Offset.Minutes() > 0 {
						stopDeperatureTime = stopDeperatureTime.Add(blockRealtimeJourney.Offset)
					}
					timetableRecordType = TimetableRecordTypeEstimated
				}
			}

			timetable = append(timetable, &TimetableRecord{
				Journey:            journey,
				Time:               stopDeperatureTime,
				DestinationDisplay: destinationDisplay,
				Type:               timetableRecordType,
			})
		}
	}

	return timetable
}
