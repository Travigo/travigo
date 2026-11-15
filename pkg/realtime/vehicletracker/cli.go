package vehicletracker

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "vehicle-tracker",
		Usage: "Realtime engine ingests location data and tracks vehicle journeys",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance of the realtime engine",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "consumers", EnvVars: []string{"TRAVIGO_TRACKER_CONSUMERS"}, Value: numConsumers},
					&cli.IntFlag{Name: "journey-cache-mib", EnvVars: []string{"TRAVIGO_TRACKER_JOURNEY_CACHE_MIB"}, Value: 768},
				},
				Action: func(c *cli.Context) error {
					workers, cacheMiB := c.Int("consumers"), c.Int("journey-cache-mib")
					if workers < 1 || workers > 256 || cacheMiB < 1 || cacheMiB > 65536 {
						return fmt.Errorf("tracker requires 1 to 256 consumers and a journey cache budget of 1 to 65536 MiB")
					}
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					StartConsumers(workers, int64(cacheMiB)<<20)

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
		},
	}
}
