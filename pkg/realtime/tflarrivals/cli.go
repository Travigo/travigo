package tflarrivals

import (
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
	"os"
	"time"
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

					file, err := os.Open("/Users/aaronclaydon/projects/travigo/test-data/victoria-arrivals.json")
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to open file")
					}
					defer file.Close()

					redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(2*time.Hour))

					realtimeJourneysStore := cache.New[string](redisStore)

					tracker := Linetracker{
						LineID:       "victoria",
						JourneyStore: realtimeJourneysStore,
					}
					tracker.ParseArrivals(file)

					return nil
				},
			},
		},
	}
}
