package realtime

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
	"os"
	"os/signal"
	"syscall"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "realtime",
		Usage: "Realtime engine ingests location data and tracks vehicle journeys",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance of the realtime engine",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					ctdf.LoadSpecialDayCache()

					StartConsumers()

					StartStatsServer()

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
						return err
					}

					StartCleaner()

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
						return err
					}

					archiver := Archiver{
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
}
