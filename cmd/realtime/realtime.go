package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/urfave/cli/v2"

	_ "time/tzdata"
)

func main() {
	if os.Getenv("TRAVIGO_LOG_FORMAT") != "JSON" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	if os.Getenv("TRAVIGO_DEBUG") == "YES" {
		log.Logger = log.Logger.Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Logger.Level(zerolog.InfoLevel)
	}

	transforms.SetupClient()

	app := &cli.App{
		Name: "realtime",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance of the realtime engine",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to redis")
					}
					if err := elastic_client.Connect(false); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Elasticsearch")
					}

					ctdf.LoadSpecialDayCache()

					// journey, erro := ctdf.IdentifyJourney(map[string]string{"BlockRef": "208461", "DestinationRef": "GB:ATCO:0100BRA10811", "DirectionRef": "inbound", "FramedVehicleJourneyDate": "2023-02-11", "OperatorRef": "GB:NOC:FBRI", "OriginAimedDepartureTime": "2023-02-11T23:20:00+00:00", "OriginRef": "GB:ATCO:0170SGB20205", "PublishedLineName": "42", "ServiceNameRef": "42", "VehicleJourneyRef": "2320"})
					// pretty.Println(journey)
					// pretty.Println(erro)

					// return nil

					realtime.StartConsumers()

					realtime.StartStatsServer()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					<-redis_client.QueueConnection.StopAllConsuming() // wait for all Consume() calls to finish

					return nil
				},
			},
			{
				Name:  "cleaner",
				Usage: "run an the queue cleaner for the realtime queue",
				Action: func(c *cli.Context) error {
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to redis")
					}

					realtime.StartCleaner()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					<-redis_client.QueueConnection.StopAllConsuming() // wait for all Consume() calls to finish

					return nil
				},
			},
			{
				Name:  "archive",
				Usage: "run archiver - takes realtime journeys out of database and puts in object store",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "output-directory",
						Usage:    "Directory to write output files to",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}

					archiver := realtime.Archiver{
						OutputDirectory:     c.String("output-directory"),
						WriteIndividualFile: false,
						WriteBundle:         true,
						CloudUpload:         true,
						CloudBucketName:     "britbus-journey-history",
					}
					archiver.Perform()

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
