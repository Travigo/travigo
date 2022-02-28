package main

import (
	"context"
	"math"
	"os"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/kr/pretty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
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

					vehicleLocation := ctdf.Location{
						Type:        "Point",
						Coordinates: []float64{0.1725319, 52.1852036},
					}

					journeysCollection := database.GetCollection("journeys")
					var journey *ctdf.Journey
					journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": "GB:NOC:SCCM:PF0000459:27:SCCM:PF0000459:27:1::VJ300"}).Decode(&journey)

					closestDistance := 999999999999.0
					var closestDistanceJourneyPath *ctdf.JourneyPathItem

					for _, journeyPathItem := range journey.Path {
						journeyPathClosestDistance := 99999999999999.0 // TODO do this better

						for i := 0; i < len(journeyPathItem.Track)-1; i++ {
							a := journeyPathItem.Track[i]
							b := journeyPathItem.Track[i+1]

							distance := pDistance(vehicleLocation, a, b)

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

func pDistance(p ctdf.Location, a ctdf.Location, b ctdf.Location) float64 {
	A := p.Coordinates[0] - a.Coordinates[0]
	B := p.Coordinates[1] - a.Coordinates[1]
	C := b.Coordinates[0] - a.Coordinates[0]
	D := b.Coordinates[1] - b.Coordinates[1]

	dot := A*C + B*D
	len_sq := C*C + D*D

	var param float64
	param = -1
	if len_sq != 0 {
		param = dot / len_sq
	}

	var xx, yy float64

	if param < 0 {
		xx = a.Coordinates[0]
		yy = a.Coordinates[1]
	} else if param > 1 {
		xx = b.Coordinates[0]
		yy = b.Coordinates[1]
	} else {
		xx = a.Coordinates[0] + param*C
		yy = a.Coordinates[1] + param*D
	}

	var dx = p.Coordinates[0] - xx
	var dy = p.Coordinates[1] - yy
	return math.Sqrt(dx*dx + dy*dy)
}
