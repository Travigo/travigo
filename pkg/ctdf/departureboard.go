package ctdf

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/exp/slices"
)

type DepartureBoard struct {
	Journey            *Journey                 `groups:"basic,departures-llm"`
	DestinationDisplay string                   `groups:"basic,departures-llm"`
	Type               DepartureBoardRecordType `groups:"basic,departures-llm"`

	Platform     string `groups:"basic,departures-llm"`
	PlatformType string `groups:"basic,departures-llm"`

	Time time.Time `groups:"basic,departures-llm"`
}

type DepartureBoardRecordType string

const (
	DepartureBoardRecordTypeScheduled       DepartureBoardRecordType = "Scheduled"
	DepartureBoardRecordTypeRealtimeTracked                          = "RealtimeTracked"
	DepartureBoardRecordTypeEstimated                                = "Estimated"
	DepartureBoardRecordTypeCancelled                                = "Cancelled"
)

func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool) []*DepartureBoard {
	journeysCollection := database.GetCollection("journeys")
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	realtimeJourneyOptions := options.FindOne().SetProjection(bson.D{
		bson.E{Key: "activelytracked", Value: 1},
		bson.E{Key: "modificationdatetime", Value: 1},
		bson.E{Key: "timeoutdurationminutes", Value: 1},
		bson.E{Key: "stops", Value: 1},
		// bson.E{Key: "stops.*.cancelled", Value: 1},
		// bson.E{Key: "stops.*.platform", Value: 1},
		// bson.E{Key: "stops.*.departuretime", Value: 1},
		bson.E{Key: "cancelled", Value: 1},
		bson.E{Key: "vehiclelocation", Value: 1},
		bson.E{Key: "journey.path.destinationstopref", Value: 1},
		bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
	})

	journeys = FilterIdenticalJourneys(journeys, true)

	p := pool.NewWithResults[*DepartureBoard]()
	p.WithMaxGoroutines(200)

	for _, journey := range journeys {
		p.Go(func() *DepartureBoard {
			var stopDepartureTime time.Time
			var stopPlatform string
			var stopPlatformType string
			var destinationDisplay string
			departureBoardRecordType := DepartureBoardRecordTypeScheduled

			if journey.Availability.MatchDate(dateTime) {
				journey.GetRealtimeJourney(realtimeJourneyOptions)

				for _, path := range journey.Path {
					if slices.Contains(stopRefs, path.OriginStopRef) {
						refTime := path.OriginDepartureTime
						stopPlatform = path.OriginPlatform
						stopPlatformType = "ESTIMATED"

						// Ignore drop off only stops from the departure board as no one should be getting onto the vehicle at this point
						if len(path.OriginActivity) == 1 && path.OriginActivity[0] == JourneyPathItemActivitySetdown {
							return nil
						}

						// Use the realtime estimated stop time based if realtime is available
						if journey.RealtimeJourney != nil {
							if journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
								if journey.RealtimeJourney.Stops[path.OriginStopRef].Cancelled {
									departureBoardRecordType = DepartureBoardRecordTypeCancelled
								}

								if journey.RealtimeJourney.ActivelyTracked {
									refTime = journey.RealtimeJourney.Stops[path.OriginStopRef].DepartureTime
								}

								if journey.RealtimeJourney.Stops[path.OriginStopRef].Platform != "" {
									stopPlatform = journey.RealtimeJourney.Stops[path.OriginStopRef].Platform
									stopPlatformType = "ACTUAL"
								}
							}

							if journey.RealtimeJourney.ActivelyTracked && departureBoardRecordType != DepartureBoardRecordTypeCancelled {
								departureBoardRecordType = DepartureBoardRecordTypeRealtimeTracked
							}
						}

						if journey.RealtimeJourney != nil && journey.RealtimeJourney.Cancelled {
							departureBoardRecordType = DepartureBoardRecordTypeCancelled
						}

						stopDepartureTime = time.Date(
							dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
						)

						destinationDisplay = path.DestinationDisplay
						break
					}
				}

				if stopDepartureTime.Before(dateTime) {
					return nil
				}

				if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
					stopPlatform = "BUS"
					stopPlatformType = "ACTUAL"
				}

				// If the departure is within 45 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
				// We estimate the current vehicle realtime journey based on the Block Number
				stopDepartureTimeFromNow := stopDepartureTime.Sub(dateTime).Minutes()
				if doEstimates &&
					departureBoardRecordType == DepartureBoardRecordTypeScheduled &&
					stopDepartureTimeFromNow <= 45 && stopDepartureTimeFromNow >= 0 &&
					journey.OtherIdentifiers["BlockNumber"] != "" {

					var blockJourneys []string
					opts := options.Find().SetProjection(bson.D{
						bson.E{Key: "primaryidentifier", Value: 1},
					})
					cursor, _ := journeysCollection.Find(context.Background(), bson.M{"serviceref": journey.ServiceRef, "otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"]}, opts)

					for cursor.Next(context.Background()) {
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
							stopDepartureTime = stopDepartureTime.Add(blockRealtimeJourney.Offset)
						}
						departureBoardRecordType = DepartureBoardRecordTypeEstimated
					}
				}

				if destinationDisplay == "" {
					lastPathItem := journey.Path[len(journey.Path)-1]
					lastPathItem.GetDestinationStop()

					if lastPathItem.DestinationStop == nil {
						destinationDisplay = "See Vehicle"
					} else {
						journey.GetService()
						lastPathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)

						destinationDisplay = lastPathItem.DestinationStop.PrimaryName
					}

				}

				return &DepartureBoard{
					Journey:            journey,
					Time:               stopDepartureTime,
					DestinationDisplay: destinationDisplay,
					Type:               departureBoardRecordType,
					Platform:           stopPlatform,
					PlatformType:       stopPlatformType,
				}
			}

			return nil
		})
	}

	departureBoardWithNil := p.Wait()
	var departureBoard []*DepartureBoard

	for _, i := range departureBoardWithNil {
		if i != nil {
			departureBoard = append(departureBoard, i)
		}
	}

	return departureBoard
}
