package ctdf

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/exp/slices"
)

type DepartureBoard struct {
	Journey            *Journey                 `groups:"basic"`
	DestinationDisplay string                   `groups:"basic"`
	Type               DepartureBoardRecordType `groups:"basic"`

	Platform     string `groups:"basic"`
	PlatformType string `groups:"basic"`

	Time time.Time `groups:"basic"`
}

type DepartureBoardRecordType string

const (
	DepartureBoardRecordTypeScheduled       DepartureBoardRecordType = "Scheduled"
	DepartureBoardRecordTypeRealtimeTracked                          = "RealtimeTracked"
	DepartureBoardRecordTypeEstimated                                = "Estimated"
	DepartureBoardRecordTypeCancelled                                = "Cancelled"
)

func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool) []*DepartureBoard {
	var departureBoard []*DepartureBoard
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
			var stopPlatform string
			var stopPlatformType string
			var destinationDisplay string
			departureBoardRecordType := DepartureBoardRecordTypeScheduled

			journey.GetRealtimeJourney()

			for _, path := range journey.Path {
				if slices.Contains(stopRefs, path.OriginStopRef) {
					refTime := path.OriginDepartureTime
					stopPlatform = path.OriginPlatform
					stopPlatformType = "ESTIMATED"

					// Use the realtime estimated stop time based if realtime is available
					if journey.RealtimeJourney != nil {
						if journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
							if journey.RealtimeJourney.ActivelyTracked {
								refTime = journey.RealtimeJourney.Stops[path.OriginStopRef].DepartureTime
							}

							if journey.RealtimeJourney.Stops[path.OriginStopRef].Platform != "" {
								stopPlatform = journey.RealtimeJourney.Stops[path.OriginStopRef].Platform
								stopPlatformType = "ACTUAL"
							}
						}

						if journey.RealtimeJourney.ActivelyTracked {
							departureBoardRecordType = DepartureBoardRecordTypeRealtimeTracked
						}
					}

					if journey.RealtimeJourney != nil && journey.RealtimeJourney.Cancelled {
						departureBoardRecordType = DepartureBoardRecordTypeCancelled
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

					var blockJourneys []string
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
					Platform:           stopPlatform,
					PlatformType:       stopPlatformType,
				})
				departureBoardGenerationMutex.Unlock()
			}
		}(journey)

	}

	wg.Wait()

	return departureBoard
}
