package ctdf

import (
	"time"
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
	// journeysCollection := database.GetCollection("journeys")
	// stopsCollection := database.GetCollection("stops")

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
			// This idea might not actually work :(
			/*
				// If the departure is within 90 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
				// We estimate the current vehicle realtime journey based on when the bus may turn around and switch journeys
				stopDeperatureTimeFromNow := stopDeperatureTime.Sub(dateTime).Minutes()
				if doEstimates && timetableRecordType == TimetableRecordTypeScheduled && stopDeperatureTimeFromNow <= 90 && stopDeperatureTimeFromNow >= 0 {
					lastPathItem := journey.Path[len(journey.Path)-1]
					numPotentials := 0

					lastPathItem.GetDestinationStop()
					associatedStopRef := lastPathItem.DestinationStopRef

					if len(lastPathItem.DestinationStop.Associations) == 1 && lastPathItem.DestinationStop.Associations[0].Type == "stop_group" {
						var associatedStop *Stop
						stopsCollection.FindOne(context.Background(),
							bson.M{"$and": bson.A{
								bson.M{"associations.associatedidentifier": lastPathItem.DestinationStop.Associations[0].AssociatedIdentifier},
								bson.M{"primaryidentifier": bson.M{"$ne": lastPathItem.DestinationStopRef}},
							}},
						).Decode(&associatedStop)

						if associatedStop != nil {
							associatedStopRef = associatedStop.PrimaryIdentifier
						}
					}

					cursor, _ := journeysCollection.Find(context.Background(), bson.M{"$and": bson.A{
						// bson.M{"serviceref": journey.ServiceRef},
						bson.M{"path.originstopref": associatedStopRef},
					}})

					for cursor.Next(context.TODO()) {
						var journey Journey
						err := cursor.Decode(&journey)
						if err != nil {
							log.Error().Err(err).Msg("Failed to decode Stop")
							continue
						}

						// Its more effecient for the db to do the query on all journeys containing this stop and then checking if its the first in code
						// The index isnt used for array index based searches
						if journey.Path[0].OriginStopRef == associatedStopRef {
							numPotentials += 1
						}
					}

					pretty.Println(lastPathItem.DestinationStopRef, lastPathItem.DestinationArrivalTime, numPotentials, associatedStopRef)

					timetableRecordType = TimetableRecordTypeEstimated
				}
			*/
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
