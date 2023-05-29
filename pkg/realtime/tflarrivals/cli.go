package tflarrivals

import (
	"encoding/json"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "tfl-arrivals",
		Usage: "Track vehicle progress through the TfL arrivals API",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance an instance of vehicle tracker",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					trackerManager := TrackerManager{
						Lines: []TfLLine{
							{
								LineID:        "victoria",
								LineName:      "Victoria",
								TransportType: ctdf.TransportTypeMetro,
							},
							{
								LineID:        "northern",
								LineName:      "Northern",
								TransportType: ctdf.TransportTypeMetro,
							},
						},
					}
					trackerManager.Run()

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
			{
				Name:  "test",
				Usage: "run an instance an instance of vehicle tracker",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					file, err := os.Open("/Users/aaronclaydon/projects/travigo/test-data/victoria-arrivals.json")
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to open file")
					}
					defer file.Close()

					jsonBytes, _ := io.ReadAll(file)

					var lineArrivals []ArrivalPrediction
					json.Unmarshal(jsonBytes, &lineArrivals)

					tracker := LineTracker{
						Line: TfLLine{
							LineID:        "victoria",
							LineName:      "Victoria",
							TransportType: ctdf.TransportTypeMetro,
						},
					}
					tracker.ParseArrivals(lineArrivals)

					return nil
				},
			},
		},
	}
}
