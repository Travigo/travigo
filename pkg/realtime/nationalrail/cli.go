package nationalrail

import (
	"os"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "national-rail",
		Usage: "Track trains using Darwin Push Port",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run an instance an instance of train tracker",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					log.Info().Msg("Starting National Rail train tracker")

					file, err := os.Open("/Users/aaronclaydon/Downloads/pPortData.log.2023-06-27_1953")
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to open file")
					}
					defer file.Close()

					ParseXMLFile(file)

					return nil
				},
			},
		},
	}
}
