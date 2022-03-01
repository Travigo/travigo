package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/kr/pretty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	app := &cli.App{
		Name: "realtime",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run the realtime calculation server",
				Flags: []cli.Flag{},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}

					journeyID := "GB:NOC:SCCM:PF0000459:27:SCCM:PF0000459:27:1::VJ300"
					timeframe := "2022-03-01"
					vehicleLocation := ctdf.Location{
						Type:        "Point",
						Coordinates: []float64{0.1725319, 52.1852036},
					}

					journeysCollection := database.GetCollection("journeys")
					var journey *ctdf.Journey
					journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}).Decode(&journey)

					closestDistance := 999999999999.0
					var closestDistanceJourneyPath *ctdf.JourneyPathItem

					for _, journeyPathItem := range journey.Path {
						journeyPathClosestDistance := 99999999999999.0 // TODO do this better

						for i := 0; i < len(journeyPathItem.Track)-1; i++ {
							a := journeyPathItem.Track[i]
							b := journeyPathItem.Track[i+1]

							distance := vehicleLocation.DistanceFromLine(a, b)

							if distance < journeyPathClosestDistance {
								journeyPathClosestDistance = distance
							}
						}

						if journeyPathClosestDistance < closestDistance {
							closestDistance = journeyPathClosestDistance
							closestDistanceJourneyPath = journeyPathItem
						}
					}

					pretty.Println(closestDistanceJourneyPath.OriginStopRef, closestDistanceJourneyPath.DestinationStopRef)

					// Update database
					realtimeJourneyIdentifier := fmt.Sprintf("REALTIME:%s:%s", timeframe, journeyID)

					realtimeJourneysCollection := database.GetCollection("realtime_journeys")
					searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

					var realtimeJourney *ctdf.RealtimeJourney
					realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&journey)

					if realtimeJourney == nil {
						realtimeJourney = &ctdf.RealtimeJourney{
							RealtimeJourneyID: realtimeJourneyIdentifier,
							JourneyRef:        journeyID,

							CreationDateTime: time.Now(),
							// DataSource: ,

							StopHistory: []*ctdf.RealtimeJourneyStopHistory{},
						}
					}

					realtimeJourney.ModificationDateTime = time.Now()
					realtimeJourney.VehicleLocation = vehicleLocation
					realtimeJourney.VehicleBearing = 0.0 //TODO real
					realtimeJourney.DepartedStopRef = closestDistanceJourneyPath.OriginStopRef
					realtimeJourney.NextStopRef = closestDistanceJourneyPath.DestinationStopRef

					opts := options.Update().SetUpsert(true)
					realtimeJourneysCollection.UpdateOne(context.Background(), searchQuery, bson.M{"$set": realtimeJourney}, opts)

					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
}
