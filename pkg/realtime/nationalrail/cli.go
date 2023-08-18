package nationalrail

import (
	"io"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/darwin"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/nrod"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/util"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "national-rail",
		Usage: "Track GB trains using Darwin Push Port & Network Rail",
		Subcommands: []*cli.Command{
			{
				Name:  "darwin",
				Usage: "run an instance an instance of the Darwin train tracker",
				Action: func(c *cli.Context) error {
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME must be set")
					}
					if env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD must be set")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					log.Info().Msg("Starting National Rail Darwin Push Port train tracker")

					stompClient := darwin.StompClient{
						Address:   "darwin-dist-44ae45.nationalrail.co.uk:61613",
						Username:  env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME"],
						Password:  env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD"],
						QueueName: "/topic/darwin.pushport-v16",
					}

					stompClient.Run()

					return nil
				},
			},
			{
				Name:  "nrod",
				Usage: "run an instance an instance of the Network Rail Open Data train tracker",
				Action: func(c *cli.Context) error {
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NETWORKRAIL_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_USERNAME must be set")
					}
					if env["TRAVIGO_NETWORKRAIL_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_PASSWORD must be set")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					log.Info().Msg("Starting Network Rail Open Data train tracker")

					file, err := os.Open("/Users/aaronclaydon/projects/travigo/test-data/nrod.json")
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to open file")
					}
					defer file.Close()

					bytes, _ := io.ReadAll(file)
					nrod.ParseMessages(bytes)

					return nil

					stompClient := nrod.StompClient{
						Address:   "publicdatafeeds.networkrail.co.uk:61618",
						Username:  env["TRAVIGO_NETWORKRAIL_USERNAME"],
						Password:  env["TRAVIGO_NETWORKRAIL_PASSWORD"],
						QueueName: "/topic/TRAIN_MVT_ALL_TOC",
					}

					stompClient.Run()

					return nil
				},
			},
		},
	}
}
