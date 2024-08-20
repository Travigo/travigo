package dbwatch

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "dbwatch",
		Usage: "Watches the database and raises events",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run events server",
				Action: func(c *cli.Context) error {
					if err := redis_client.Connect(); err != nil {
						return err
					}

					log.Info().Msg("Starting dbwatch server")

					serviceAlerts := NewServiceAlertsWatch()
					go serviceAlerts.Run()

					realtimeJourneys := NewRealtimeJourneysWatch()
					go realtimeJourneys.Run()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					return nil
				},
			},
		},
	}
}
