package ctdf

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DepartureBoard struct {
	Journey            *Journey                 `groups:"basic"`
	DestinationDisplay string                   `groups:"basic"`
	Type               DepartureBoardRecordType `groups:"basic"`

	Time time.Time `groups:"basic"`
}

type DepartureBoardRecordType string

const (
	DepartureBoardRecordTypeScheduled       DepartureBoardRecordType = "Scheduled"
	DepartureBoardRecordTypeRealtimeTracked                          = "RealtimeTracked"
	DepartureBoardRecordTypeEstimated                                = "Estimated"
)

func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time, realtimeTimeframe string, doEstimates bool) []*DepartureBoard {
	departureBoard := []*DepartureBoard{}
	journeysCollection := database.GetCollection("journeys")
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	journeys = FilterIdenticalJourneys(journeys, true)

	wg := &sync.WaitGroup{}
	departureBoardGenerationMutex := sync.Mutex{}

	for _, journey := range journeys {
		wg.Add(1)
		go func(journey *Journey) {
			defer wg.Done()

			var stopDeperatureTime time.Time
			var destinationDisplay string
			departureBoardRecordType := DepartureBoardRecordTypeScheduled

			journey.GetRealtimeJourney(realtimeTimeframe)

			for _, path := range journey.Path {
				if path.OriginStopRef == stopRef {
					refTime := path.OriginDepartureTime

					// Use the realtime estimated stop time based if realtime is available
					if journey.RealtimeJourney != nil && journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
						refTime = journey.RealtimeJourney.Stops[path.OriginStopRef].DepartureTime
						departureBoardRecordType = DepartureBoardRecordTypeRealtimeTracked
					}

					stopDeperatureTime = time.Date(
						dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
					)

					destinationDisplay = path.DestinationDisplay
					break
				}
			}

			if stopDeperatureTime.Before(dateTime) {
				return
			}

			availability := journey.Availability

			if availability.MatchDate(dateTime) {
				journey.GetReferences()

				// If the departure is within 45 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
				// We estimate the current vehicle realtime journey based on the Block Number
				stopDeperatureTimeFromNow := stopDeperatureTime.Sub(dateTime).Minutes()
				if doEstimates &&
					departureBoardRecordType == DepartureBoardRecordTypeScheduled &&
					stopDeperatureTimeFromNow <= 45 && stopDeperatureTimeFromNow >= 0 &&
					journey.OtherIdentifiers["BlockNumber"] != "" {

					blockJourneys := []string{}
					opts := options.Find().SetProjection(bson.D{
						bson.E{Key: "primaryidentifier", Value: 1},
					})
					cursor, _ := journeysCollection.Find(context.Background(), bson.M{"serviceref": journey.ServiceRef, "otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"]}, opts)

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
						departureBoardRecordType = DepartureBoardRecordTypeEstimated
					}
				}

				if destinationDisplay == "" {
					lastPathItem := journey.Path[len(journey.Path)-1]
					lastPathItem.GetDestinationStop()
					lastPathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)

					destinationDisplay = lastPathItem.DestinationStop.PrimaryName
				}

				departureBoardGenerationMutex.Lock()
				departureBoard = append(departureBoard, &DepartureBoard{
					Journey:            journey,
					Time:               stopDeperatureTime,
					DestinationDisplay: destinationDisplay,
					Type:               departureBoardRecordType,
				})
				departureBoardGenerationMutex.Unlock()
			}
		}(journey)

	}

	wg.Wait()

	return departureBoard
}
